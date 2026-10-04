import os
from PyQt6.QtCore import QLockFile, QStandardPaths
from PyQt6.QtNetwork import QLocalServer, QLocalSocket


class AppInstance:
    def __init__(self):
        runtime = QStandardPaths.writableLocation(QStandardPaths.StandardLocation.RuntimeLocation)
        self.address = os.path.join(runtime, "git-gui.socket")
        self.lock = QLockFile(self.address + ".lock")
        self.server = QLocalServer()

    def acquire(self):
        if not self.lock.tryLock(0):
            socket = QLocalSocket()
            socket.connectToServer(self.address)
            if not socket.waitForConnected(3000):
                raise RuntimeError("Git GUI is already starting or its window could not be reached.")
            socket.write(b"show\n")
            socket.waitForBytesWritten(1000)
            socket.disconnectFromServer()
            return False
        QLocalServer.removeServer(self.address)
        if not self.server.listen(self.address):
            raise RuntimeError(self.server.errorString())
        return True

    def on_activation(self, callback):
        def activate():
            while self.server.hasPendingConnections():
                socket = self.server.nextPendingConnection()
                socket.disconnectFromServer()
                socket.deleteLater()
            callback()
        self.server.newConnection.connect(activate)
