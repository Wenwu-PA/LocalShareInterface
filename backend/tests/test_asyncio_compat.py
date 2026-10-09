from __future__ import annotations

import sys
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

from backend.network.asyncio_compat import _wrap_connection_lost, install_windows_reset_cleanup


def reset_error(code=10054):
    error = ConnectionResetError(code, "peer reset")
    error.winerror = code
    return error


class WindowsResetCleanupTests(unittest.TestCase):
    def setUp(self):
        self.transport = SimpleNamespace(_sock=Mock(), _server=Mock(), _called_connection_lost=False)

    def test_shutdown_reset_finishes_socket_and_server_cleanup(self):
        error = reset_error()

        def original(transport, exc):
            raise error

        sock, server = self.transport._sock, self.transport._server
        _wrap_connection_lost(original)(self.transport, None)
        sock.close.assert_called_once_with()
        server._detach.assert_called_once_with(self.transport)
        self.assertIsNone(self.transport._sock)
        self.assertIsNone(self.transport._server)
        self.assertTrue(self.transport._called_connection_lost)

    def test_client_transport_without_server(self):
        self.transport._server = None

        def original(transport, exc):
            raise reset_error()

        _wrap_connection_lost(original)(self.transport, None)
        self.assertTrue(self.transport._called_connection_lost)

    def test_protocol_reset_is_not_hidden(self):
        def protocol_callback():
            raise reset_error()

        def original(transport, exc):
            protocol_callback()

        with self.assertRaises(ConnectionResetError):
            _wrap_connection_lost(original)(self.transport, None)
        self.transport._sock.close.assert_not_called()

    def test_other_errors_are_not_hidden(self):
        for error in (reset_error(10053), ConnectionResetError("no Windows code"), RuntimeError("application bug")):
            with self.subTest(error=error):
                def original(transport, exc):
                    raise error

                with self.assertRaises(type(error)):
                    _wrap_connection_lost(original)(self.transport, None)
        self.transport._sock.close.assert_not_called()

    def test_successful_callback_is_unchanged(self):
        original = Mock(return_value="done")
        result = _wrap_connection_lost(original)(self.transport, None)
        self.assertEqual(result, "done")
        original.assert_called_once_with(self.transport, None)
        self.transport._sock.close.assert_not_called()

    def test_completed_transport_error_is_not_hidden(self):
        self.transport._called_connection_lost = True

        def original(transport, exc):
            raise reset_error()

        with self.assertRaises(ConnectionResetError):
            _wrap_connection_lost(original)(self.transport, None)

    @unittest.skipUnless(sys.platform == "win32", "Windows Proactor only")
    def test_real_proactor_callback_closes_once(self):
        from asyncio.proactor_events import _ProactorBasePipeTransport

        protocol = self.transport._protocol = Mock()
        sock, server = self.transport._sock, self.transport._server
        callback = _wrap_connection_lost(_ProactorBasePipeTransport._call_connection_lost)
        callback(self.transport, None)
        callback(self.transport, None)
        protocol.connection_lost.assert_called_once_with(None)
        sock.close.assert_called_once_with()
        server._detach.assert_called_once_with(self.transport)

    @unittest.skipUnless(sys.platform == "win32", "Windows Proactor only")
    def test_install_is_idempotent(self):
        from asyncio.proactor_events import _ProactorBasePipeTransport

        original = _ProactorBasePipeTransport._call_connection_lost
        with patch.object(_ProactorBasePipeTransport, "_call_connection_lost", original):
            install_windows_reset_cleanup()
            installed = _ProactorBasePipeTransport._call_connection_lost
            install_windows_reset_cleanup()
            self.assertIs(_ProactorBasePipeTransport._call_connection_lost, installed)

    def test_non_windows_is_unchanged(self):
        with patch("backend.network.asyncio_compat.sys.platform", "linux"):
            install_windows_reset_cleanup()
