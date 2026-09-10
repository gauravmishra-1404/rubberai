"""Dependency-free example SDK. Background delivery never raises into agent code.

Use a localhost Go collector URL for durable offline buffering. A direct server
URL is also supported, with a bounded in-memory retry queue.
"""
import json
import queue
import threading
import time
import urllib.error
import urllib.request
import uuid
from datetime import datetime, timezone


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Client:
    def __init__(self, endpoint, api_key, project_id, capacity=256, timeout=5):
        self.endpoint = endpoint.rstrip('/') + '/api/v1/events'
        self._key = api_key
        self.project_id = project_id
        self.timeout = timeout
        self._queue = queue.Queue(maxsize=capacity)
        self._stop = threading.Event()
        self.dropped = 0
        self.delivered = 0
        self.rejected = 0
        self._opener = urllib.request.build_opener(NoRedirect())
        self._worker = threading.Thread(target=self._run, daemon=True)
        self._worker.start()

    def emit(self, event_type, **fields):
        """Return False if closed, malformed, or full. No network I/O here."""
        if self._stop.is_set():
            return False
        event = dict(fields)
        event.setdefault('event_id', 'evt_' + uuid.uuid4().hex)
        event.setdefault('timestamp', datetime.now(timezone.utc).isoformat())
        event.update(event_type=event_type, project_id=self.project_id)
        try:
            body = json.dumps(event).encode()
            if len(body) > 1_048_576:
                self.dropped += 1
                return False
            self._queue.put_nowait(body)
            return True
        except (queue.Full, TypeError, ValueError):
            self.dropped += 1
            return False

    def _run(self):
        while not self._stop.is_set():
            try:
                body = self._queue.get(timeout=0.1)
            except queue.Empty:
                continue
            delay = 0.25
            try:
                while not self._stop.is_set():
                    try:
                        req = urllib.request.Request(self.endpoint, data=body, headers={
                            'Content-Type': 'application/json',
                            'Authorization': 'Bearer ' + self._key,
                        })
                        with self._opener.open(req, timeout=self.timeout) as response:
                            response.read(4096)
                        self.delivered += 1
                        break
                    except urllib.error.HTTPError as exc:
                        if exc.code in (400, 403, 404, 413, 415, 422):
                            self.rejected += 1
                            break
                    except (urllib.error.URLError, OSError, TimeoutError):
                        pass
                    if self._stop.wait(delay):
                        break
                    delay = min(delay * 2, 60)
            finally:
                self._queue.task_done()

    def close(self, timeout=2):
        """Best-effort bounded flush. Returns whether all events were delivered."""
        end = time.monotonic() + timeout
        while self._queue.unfinished_tasks and time.monotonic() < end:
            time.sleep(0.01)
        flushed = not self._queue.unfinished_tasks
        self._stop.set()
        self._worker.join(timeout=max(0, end - time.monotonic()))
        return flushed
