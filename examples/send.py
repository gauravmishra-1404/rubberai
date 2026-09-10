"""Run against a local rubberai instance; no dependencies needed."""
import json
import os
import urllib.request
import uuid
from datetime import datetime, timezone

event = {
    'event_id': 'evt_' + uuid.uuid4().hex,
    'event_type': 'llm.request.completed',
    'project_id': os.environ['RUBBERAI_PROJECT_ID'],
    'user_id': 'example-developer',
    'session_id': 'example-session',
    'trace_id': 'example-trace',
    'prompt_id': 'example-prompt',
    'timestamp': datetime.now(timezone.utc).isoformat(),
    'agent': {'name': 'example-agent'},
    'ide': {'name': 'terminal'},
    'model': {'provider': 'example', 'name': 'model'},
    'usage': {'input_tokens': 1000, 'output_tokens': 500},
}
request = urllib.request.Request(
    os.getenv('RUBBERAI_URL', 'http://localhost:8080') + '/api/v1/events',
    data=json.dumps(event).encode(),
    headers={'Content-Type': 'application/json',
             'Authorization': 'Bearer ' + os.environ['RUBBERAI_API_KEY']},
)
with urllib.request.urlopen(request, timeout=10) as response:
    print(response.read().decode())
