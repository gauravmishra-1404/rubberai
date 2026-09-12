# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Gaurav Mishra
import io
import time
import unittest
import urllib.error
from rubberai import Client


class RetryOpener:
    def __init__(self):
        self.bodies = []

    def open(self, request, timeout):
        self.bodies.append(request.data)
        if len(self.bodies) == 1:
            raise urllib.error.URLError('offline')
        return io.BytesIO(b'{}')


class ClientTest(unittest.TestCase):
    def test_retry_preserves_id(self):
        client = Client('http://localhost:1', 'test', 'project')
        opener = RetryOpener()
        client._opener = opener
        self.assertTrue(client.emit('prompt.created', prompt_id='p1'))
        self.assertTrue(client.close(timeout=2))
        self.assertEqual(client.delivered, 1)
        self.assertEqual(opener.bodies[0], opener.bodies[1])

    def test_outage_does_not_block_emission(self):
        client = Client('http://127.0.0.1:1', 'test', 'project', capacity=2, timeout=0.1)
        start = time.monotonic()
        for _ in range(20):
            client.emit('custom.event')
        self.assertLess(time.monotonic() - start, 0.2)
        self.assertGreater(client.dropped, 0)
        self.assertFalse(client.close(timeout=0.01))

    def test_invalid_payload_is_nonfatal(self):
        client = Client('http://localhost:1', 'test', 'project')
        self.assertFalse(client.emit('custom.event', bad=object()))
        client.close()


if __name__ == '__main__':
    unittest.main()
