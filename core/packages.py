"""Read package definitions and edit their entries without rewriting recipes."""

import json
import re
from dataclasses import dataclass
from pathlib import Path

import yaml
from yaml.nodes import MappingNode, ScalarNode, SequenceNode

BUNDLE_ENTRY = re.compile(r'^\s*(brew|cask|tap|mas|vscode|go|cargo|uv|whalebrew)\s+(["\'])(.*?)\2')


@dataclass(frozen=True)
class PackageEntry:
    name: str
    start: int
    end: int


@dataclass(frozen=True)
class PackageSource:
    kind: str
    path: str
    section: tuple = ()

    @property
    def label(self):
        if not self.section:
            return self.path
        return f"{self.path} · {' / '.join(map(str, self.section))}"


def read_text(path):
    return Path(path).read_bytes().decode("utf-8")


def _child(node, key):
    if isinstance(node, SequenceNode):
        return node.value[key]
    return next(value for name, value in node.value if name.value == key)


def _node(text, section):
    node = yaml.compose(text, Loader=yaml.SafeLoader)
    for key in section:
        node = _child(node, key)
    return node


def sources_for_file(kind, path, text):
    if Path(path).suffix.lower() not in (".yml", ".yaml"):
        return [PackageSource(kind, path)]
    root = yaml.compose(text, Loader=yaml.SafeLoader)
    found = []

    def visit(node, section):
        if isinstance(node, MappingNode):
            values = {key.value: value for key, value in node.value}
            module_type = values.get("type")
            if isinstance(module_type, ScalarNode) and module_type.value in ("dnf", "rpm-ostree"):
                for operation in ("install", "remove"):
                    packages = values.get(operation)
                    address = section + (operation,)
                    if isinstance(packages, MappingNode):
                        packages = {k.value: v for k, v in packages.value}.get("packages")
                        address += ("packages",)
                    if isinstance(packages, SequenceNode):
                        found.append(PackageSource(kind, path, address))
            for key, value in node.value:
                visit(value, section + (key.value,))
        elif isinstance(node, SequenceNode):
            for index, value in enumerate(node.value):
                visit(value, section + (index,))

    if root:
        visit(root, ())
    return found


def discover_sources(repo, configured=()):
    repo = Path(repo)
    candidates = {}
    for path in sorted((repo / "recipes").rglob("*"), key=lambda path: (path.name != "recipe.yml", str(path))):
        if path.suffix.lower() in (".yml", ".yaml"):
            candidates[path.relative_to(repo).as_posix()] = "RPM"
    for folder in (Path("."), Path("manifests"), Path("packages"), Path("restore/packages")):
        for filename, kind in (("Brewfile", "Brew"), ("brew.txt", "Brew"),
                               ("rpm.txt", "RPM"), ("rpm-packages.txt", "RPM"),
                               ("nix.txt", "Nix"), ("nix-profile.txt", "Nix")):
            path = folder / filename
            if (repo / path).is_file():
                candidates[path.as_posix()] = kind
    for entry in configured:
        candidates[entry["path"]] = entry["kind"]
    found, errors = [], []
    for path, kind in candidates.items():
        try:
            found.extend(sources_for_file(kind, path, read_text(repo / path)))
        except (OSError, ValueError, yaml.YAMLError) as error:
            errors.append(f"{path}: {error}")
    return found, errors


def _line_span(text, start, end):
    start = text.rfind("\n", 0, start) + 1
    last = len(text[:end].rstrip(" \t\r\n"))
    end = text.find("\n", last)
    return start, len(text) if end < 0 else end + 1


def entries(source, text):
    if source.section:
        node = _node(text, source.section)
        result = []
        for value in node.value:
            start, end = value.start_mark.index, value.end_mark.index
            name = (value.value if isinstance(value, ScalarNode) else
                    text[start:end].strip())
            if not node.flow_style:
                start, end = _line_span(text, start, end)
            result.append(PackageEntry(name, start, end))
        return result
    result, offset = [], 0
    for line in text.splitlines(keepends=True):
        name = line.strip()
        if name and not name.startswith("#"):
            if Path(source.path).name == "Brewfile":
                if match := BUNDLE_ENTRY.match(line):
                    name = f"{match[1]} {match[3]}"
                    result.append(PackageEntry(name, offset, offset + len(line)))
            else:
                name = re.split(r"\s+#", name, maxsplit=1)[0].rstrip()
                result.append(PackageEntry(name, offset, offset + len(line)))
        offset += len(line)
    return result


def entry_name(source, value):
    if Path(source.path).name == "Brewfile":
        match = BUNDLE_ENTRY.match(value)
        return f"{match[1]} {match[3]}" if match else f"brew {value}"
    return value


def edit_entries(source, text, *, add="", remove=()):
    current = entries(source, text)
    if source.section:
        node = _node(text, source.section)
        if node.flow_style:
            values = yaml.safe_load(text)
            for key in source.section:
                values = values[key]
            values = [value for index, value in enumerate(values) if index not in remove]
            if add:
                values.append(add)
            replacement = yaml.safe_dump(values, default_flow_style=True, sort_keys=False, width=100000).strip()
            return text[:node.start_mark.index] + replacement + text[node.end_mark.index:]
        if add:
            newline = "\r\n" if "\r\n" in text else "\n"
            value = json.dumps(add, ensure_ascii=False)
            if current:
                position = current[-1].end
                indent = " " * node.start_mark.column
                prefix = "" if text[:position].endswith("\n") else newline
                return text[:position] + prefix + indent + "- " + value + newline + text[position:]
            return text[:node.start_mark.index] + "[" + value + "]" + text[node.end_mark.index:]
    elif add:
        if Path(source.path).name == "Brewfile" and not BUNDLE_ENTRY.match(add):
            add = "brew " + json.dumps(add, ensure_ascii=False)
        newline = "\r\n" if "\r\n" in text else "\n"
        return text + (newline if text and not text.endswith("\n") else "") + add + newline
    for index in sorted(remove, reverse=True):
        entry = current[index]
        replacement = ""
        if source.section and len(remove) == len(current) and index == min(remove):
            replacement = " " * node.start_mark.column + "[]\n"
        text = text[:entry.start] + replacement + text[entry.end:]
    return text


def commit_subject(added, removed):
    def describe(names):
        if len(names) == 1:
            name = next(iter(names))
            if name.startswith("brew "):
                name = name.removeprefix("brew ")
            if "#" in name:
                name = name.rsplit("#", 1)[1].split(".")[-1]
            if len(name) <= 48 and "\n" not in name:
                return name
        return f"{len(names)} package" + ("s" if len(names) != 1 else "")

    if added and removed:
        return "chore(packages): update package selection"
    if added:
        return f"feat(packages): add {describe(added)}"
    if removed:
        return f"chore(packages): remove {describe(removed)}"
    return ""
