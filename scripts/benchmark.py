# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Gaurav Mishra
"""Small localhost smoke benchmark. Never point at a production server."""
import json, os, time, uuid, statistics, urllib.request, http.cookiejar
from datetime import datetime, timezone
base=os.getenv('RUBBERAI_TEST_URL','http://localhost:8093')
if not base.startswith(('http://localhost:','http://127.0.0.1:')):
    raise SystemExit('This benchmark requires a localhost test instance')
client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def call(path,body=None,key=None):
    data=None if body is None else json.dumps(body).encode()
    headers={'Content-Type':'application/json'}
    if key:headers['Authorization']='Bearer '+key
    with client.open(urllib.request.Request(base+'/api/v1'+path,data=data,headers=headers),timeout=20) as r:
        return json.load(r)
call('/auth/register',{'username':'bench_'+uuid.uuid4().hex,'password':'local-benchmark-password','display_name':'Benchmark','organization':'Isolated benchmark'})
project=call('/projects',{'name':'Ingestion smoke benchmark'})['id']
key=call('/projects/'+project+'/keys',{'name':'Benchmark'})['api_key']
timings=[]
for batch in range(30):
    events=[{'event_id':str(uuid.uuid4()),'event_type':'llm.request.completed','project_id':project,'user_id':'benchmark','session_id':'session_'+str(batch),'trace_id':'trace_'+str(batch),'timestamp':datetime.now(timezone.utc).isoformat(),'usage':{'input_tokens':100,'output_tokens':50}} for _ in range(100)]
    start=time.perf_counter(); result=call('/events',{'events':events},key); timings.append(time.perf_counter()-start)
    assert result['accepted']==100
start=time.perf_counter();stats=call('/projects/'+project+'/analytics');analytics_ms=(time.perf_counter()-start)*1000
assert stats['totals']['total_tokens']==450000
print(json.dumps({'events':3000,'batch_size':100,'concurrency':1,'mean_batch_ms':round(statistics.mean(timings)*1000,2),'p95_batch_ms':round(sorted(timings)[28]*1000,2),'events_per_second':round(3000/sum(timings),1),'analytics_ms':round(analytics_ms,2)},indent=2))
