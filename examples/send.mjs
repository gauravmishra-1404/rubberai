// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Gaurav Mishra
import { randomUUID } from 'node:crypto';
const event={event_id:randomUUID(),event_type:'prompt.created',project_id:process.env.RUBBERAI_PROJECT_ID,user_id:'js-developer',session_id:'example-session',trace_id:'example-trace',prompt_id:randomUUID(),timestamp:new Date().toISOString(),agent:{name:'custom-agent'},ide:{name:'terminal'}};
const response=await fetch(`${process.env.RUBBERAI_URL||'http://localhost:8080'}/api/v1/events`,{method:'POST',headers:{'Content-Type':'application/json',Authorization:`Bearer ${process.env.RUBBERAI_API_KEY}`},body:JSON.stringify(event),signal:AbortSignal.timeout(10000),redirect:'error'});
if(!response.ok)throw new Error(`Ingestion returned ${response.status}`);
console.log(await response.json());
