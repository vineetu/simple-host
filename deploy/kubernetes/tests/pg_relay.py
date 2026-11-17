"""Forward a disposable kind node's TCP port to the mounted test DB socket."""
import select
import socket
import socketserver
import sys


class Relay(socketserver.BaseRequestHandler):
    def handle(self):
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as upstream:
            upstream.connect(sys.argv[1])
            peers = (self.request, upstream)
            while True:
                readable, _, _ = select.select(peers, [], [], 1)
                for source in readable:
                    data = source.recv(65536)
                    if not data:
                        return
                    (upstream if source is self.request else self.request).sendall(data)


with socketserver.ThreadingTCPServer(('0.0.0.0', int(sys.argv[2])), Relay) as server:
    server.daemon_threads = True
    server.serve_forever()
