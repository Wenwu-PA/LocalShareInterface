"""Work around incomplete Proactor socket cleanup after a Windows peer reset."""
from __future__ import annotations

from functools import wraps
import sys


def _wrap_connection_lost(original):
    @wraps(original)
    def connection_lost(transport, exc):
        try:
            return original(transport, exc)
        except ConnectionResetError as error:
            traceback = error.__traceback__
            while traceback.tb_next is not None:
                traceback = traceback.tb_next
            # Only the socket shutdown inside CPython's callback is recoverable.
            # Protocol errors, other callbacks and other Windows errors propagate.
            if (getattr(error, "winerror", None) != 10054
                    or traceback.tb_frame.f_code is not original.__code__
                    or transport._called_connection_lost
                    or transport._sock is None):
                raise
            transport._sock.close()
            transport._sock = None
            if transport._server is not None:
                transport._server._detach(transport)
                transport._server = None
            transport._called_connection_lost = True

    connection_lost._lanbridge_reset_cleanup = True
    return connection_lost


def install_windows_reset_cleanup() -> None:
    """Install once per worker; retain Proactor support for Windows subprocesses."""
    if sys.platform != "win32":
        return
    from asyncio.proactor_events import _ProactorBasePipeTransport

    original = _ProactorBasePipeTransport._call_connection_lost
    if not getattr(original, "_lanbridge_reset_cleanup", False):
        _ProactorBasePipeTransport._call_connection_lost = _wrap_connection_lost(original)
