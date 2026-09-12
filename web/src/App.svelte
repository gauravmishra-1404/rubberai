<!--
  SPDX-License-Identifier: MIT
  Copyright (c) 2026 Gaurav Mishra
-->
<script lang="ts">
 import { onMount } from 'svelte';
 type Project={id:string;name:string;description:string;repository_url:string;privacy:string;track_diffs:boolean};
 type Activity={event_id:string;event_type:string;timestamp:string;user_id?:string;session_id?:string;trace_id?:string;prompt_id?:string;prompt?:string;file?:{path:string;change_source?:string;operation?:string;diff?:string;lines_added?:number;lines_removed?:number};agent?:{name?:string};model?:{name?:string};usage?:Record<string,number>;[key:string]:unknown};
 type Stats={events:number;prompts:number;requests:number;total_tokens:number|null;sessions:number;traces:number;files_changed:number;lines_added:number;lines_removed:number;requests_with_total:number;requests_with_cost:number;tools:number;prompt_text:string|null;models:string[]|null;files:string[]|null;file_changes:FileChange[]|null;prompt_host:string|null;prompt_ip:string|null;prompt_email:string|null;prompt_summary:string|null;started_at:string|null;ended_at:string|null;[key:string]:unknown};
 type Analytics={totals:Stats;costs:{amount:string;currency:string;type:string}[];breakdown:{name:string;stats:Stats;costs:{currency:string;type:string;amount:string}[]|null}[]};
 type FileChange={path:string;status:string|null;operation:string|null;lines_added:string|null;lines_removed:string|null;diff:string|null};
 type Key={id:string;name:string;revoked_at:string|null};
 let me:{id:string;display_name:string;organization_id:string;demo?:string}|null=$state(null),ready=$state(false),register=$state(true),busy=$state(false),error=$state('');
 let username=$state(''),password=$state(''),displayName=$state(''),organization=$state(''),email=$state('');
 let projects:Project[]=$state([]),selected=$state(''),newName=$state(''),view=$state('activity');
 let analytics:Analytics|null=$state(null),events:Activity[]=$state([]),keys:Key[]=$state([]),issuedKey=$state(''),keyName=$state('Local development');
 let group=$state('prompt'),userFilter=$state(''),agentFilter=$state(''),modelFilter=$state(''),traceFilter=$state(''),promptFilter=$state(''),languageFilter=$state(''),ideFilter=$state(''),sessionFilter=$state(''),repositoryFilter=$state(''),branchFilter=$state(''),eventFilter=$state('');
 let from=$state(new Date(Date.now()-30*86400000).toISOString().slice(0,10)),to=$state(new Date().toISOString().slice(0,10));
 let changeSet:{label:string;rows:FileChange[]}|null=$state(null),changeFile:FileChange|null=$state(null);
 let textSheet:{label:string;body:string}|null=$state(null);
 function openSummary(label:string,body:string){textSheet={label,body}}
 function digest(t:Stats):string{
  // A fixed shape, not prose: the same three measures in the same order on every
  // row, so a column of turns can be compared at a glance instead of read.
  const parts=[];
  if(t.tools)parts.push(t.tools+'\u2009t');
  if(t.files_changed)parts.push(t.files_changed+'\u2009f');
  if(t.lines_added||t.lines_removed)parts.push('+'+number(Number(t.lines_added))+'\u2009\u2212'+number(Number(t.lines_removed)));
  return parts.join(' \u00b7 ')||'\u2014';
 }
 function mergeChanges(rows:FileChange[]):FileChange[]{
  // One event is recorded per change, so a file edited twice in a turn arrives
  // twice. Merge by path: the totals add up, and the latest status and diff win,
  // which is what "what did this turn do to this file" means.
  const byPath=new Map<string,FileChange>();
  for(const r of rows){
   const seen=byPath.get(r.path);
   if(!seen){byPath.set(r.path,{...r});continue}
   seen.lines_added=String(Number(seen.lines_added||0)+Number(r.lines_added||0));
   seen.lines_removed=String(Number(seen.lines_removed||0)+Number(r.lines_removed||0));
   seen.status=r.status??seen.status;
   seen.operation=r.operation??seen.operation;
   seen.diff=r.diff??seen.diff;
  }
  return [...byPath.values()];
 }
 function openChanges(label:string,rows:FileChange[]|null){
  if(!rows||!rows.length)return;
  const merged=mergeChanges(rows);
  // Open on the first file rather than an empty pane, so the panel shows what it
  // is for the moment it appears.
  changeSet={label,rows:merged};changeFile=merged[0];
 }
 function closeChanges(){changeSet=null;changeFile=null;textSheet=null}
  let detail:Activity|null=$state(null),hasMore=$state(false),offset=$state(0),privacy=$state('METADATA_ONLY'),trackDiffs=$state(false);
 const project=$derived(projects.find(p=>p.id===selected));
 // People sign up with their email as their username. Asking for both then makes
 // the second field pure retyping, so it only appears when it would add something.
 const usernameIsEmail=$derived(/^[^\s@]+@[^\s@.]+\.[^\s@]+$/.test(username.trim()));
 const contactEmail=$derived(usernameIsEmail?username.trim():email);
 const tokenFields=[['input_tokens','Input'],['output_tokens','Output'],['cached_tokens','Cache read'],['cache_write_tokens','Cache write'],['reasoning_tokens','Reasoning'],['total_tokens','Total']];
 const tokens=(s:Stats,key:string)=>s[key]==null?'Not reported':number(Number(s[key]))+(Number(s['reported_'+key])<s.requests?' (partial)':'');
 const number=(v:number|null|undefined)=>new Intl.NumberFormat().format(v??0);
 async function api(path:string,method='GET',body?:unknown){const res=await fetch('/api/v1'+path,{method,headers:body===undefined?{}:{'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body)});const value=await res.json();if(!res.ok)throw new Error(value.error?.message||'Request failed');return value;}
 async function action(fn:()=>Promise<void>){busy=true;error='';try{await fn()}catch(e){error=e instanceof Error?e.message:'Request failed'}finally{busy=false}}
 function query(){const p=new URLSearchParams({from:new Date(from+'T00:00:00Z').toISOString(),to:new Date(to+'T23:59:59.999Z').toISOString(),group_by:group,user_prompts:'true'});for(const [key,value]of Object.entries({user_id:userFilter,agent:agentFilter,model:modelFilter,trace_id:traceFilter,prompt_id:promptFilter,language:languageFilter,ide:ideFilter,session_id:sessionFilter,repository:repositoryFilter,branch:branchFilter,event_type:eventFilter})){if(value)p.set(key,value)}return p}
 async function refresh(){if(!selected)return;const p=query();const [a,e,k]=await Promise.all([api(`/projects/${selected}/analytics?${p}`),api(`/projects/${selected}/events?${p}`),me?.demo?Promise.resolve([]):api(`/projects/${selected}/keys`)]);analytics=a;events=e.events;hasMore=e.has_more;offset=e.next_offset;keys=k;privacy=project?.privacy??'METADATA_ONLY';trackDiffs=project?.track_diffs??false;}
 async function loadProjects(){projects=await api('/projects');if(!selected&&projects.length)selected=projects[0].id;await refresh()}
 async function authenticate(){await action(async()=>{me=await api('/auth/'+(register?'register':'login'),'POST',{username,password,display_name:displayName,organization});password='';await loadProjects()})}
 async function createProject(){await action(async()=>{const p=await api('/projects','POST',{name:newName});selected=p.id;newName='';await loadProjects()})}
 async function selectProject(){detail=null;issuedKey='';traceFilter='';promptFilter='';await action(refresh)}
 async function inspectTrace(id:string){traceFilter=id;promptFilter='';view='activity';detail=null;await action(refresh)}
 async function inspectPrompt(id:string){eventFilter='';modelFilter='';languageFilter='';promptFilter=id;traceFilter='';view='activity';detail=null;await action(refresh)}
 onMount(()=>{(async()=>{try{me=await api('/me');await loadProjects()}catch(e){if(me)error=e instanceof Error?e.message:'Could not load projects'}finally{ready=true}})()});
</script>

<svelte:window onkeydown={e=>{if(e.key==='Escape')closeChanges()}}/>
<svelte:head><meta name="description" content="Understand AI coding activity across your projects, tools and models."/></svelte:head>
{#if !ready}<main class="loading">Loading rubberai…</main>
{:else if !me}
 <main class="welcome"><section class="pitch"><a class="brand" href="/">rubber<span>ai</span><i></i></a><div class="eyebrow">DEVELOPER OBSERVABILITY</div><h1>Every AI action.<br/>One clear picture.</h1><p>Connect prompts, tools, tokens and code changes. Understand what happened, without guessing who did it.</p><div class="principles"><span>Any agent</span><span>Any language</span><span>Privacy first</span></div></section>
 <section class="auth card"><div class="eyebrow">YOUR WORKSPACE</div><h2>{register?'Start with an organization':'Welcome back'}</h2><p class="muted">{register?'Create your account. No email address required.':'Sign in to explore your development activity.'}</p>
 <form onsubmit={e=>{e.preventDefault();authenticate()}}>
 {#if register}<label>Organization<input bind:value={organization} maxlength="120" required placeholder="Acme engineering"/></label><label>Your name<input bind:value={displayName} maxlength="120" required placeholder="Display name" autocomplete="name"/></label>{#if !usernameIsEmail}<label><span>Email <span class="optional">optional</span></span><input type="email" bind:value={email} maxlength="254" placeholder="you@example.com" autocomplete="email"/></label>{/if}{/if}
 <label>Username<input bind:value={username} required autocomplete="username" maxlength="160"/></label>{#if register&&usernameIsEmail}<p class="caption note">That is an email address, so it will be used for contact too. No need to enter it twice.</p>{/if}<label>Password<input type="password" bind:value={password} required minlength={register?12:1} maxlength="256" autocomplete={register?'new-password':'current-password'}/></label>
 {#if error}<p role="alert" class="error">{error}</p>{/if}<button class="primary" disabled={busy}>{busy?'Working…':register?'Create workspace →':'Sign in →'}</button></form>
 <button class="text-button" onclick={()=>{register=!register;error=''}}>{register?'Already have an account? Sign in':'Create an organization'}</button></section></main>
{:else}
 <div class="shell"><aside><a class="brand" href="/">rubber<span>ai</span><i></i></a><div class="eyebrow">WORKSPACE</div><label class="sr-only" for="project">Project</label><select id="project" bind:value={selected} onchange={selectProject} disabled={busy}>{#each projects as p}<option value={p.id}>{p.name}</option>{/each}</select><nav>{#each (me.demo?[['activity','Activity overview']]:[['activity','Activity overview'],['keys','Connect a project'],['settings','Privacy & settings']]) as [id,label]}<button class:active={view===id} onclick={()=>view=id}>{label}</button>{/each}</nav><div class="sidebar-bottom"><span class="avatar">{me.demo?'D':me.display_name.slice(0,1).toUpperCase()}</span><div>{me.demo?'Demo visitor':me.display_name}<small>{me.demo?'read-only':'Organization workspace'}</small></div><button class="text-button" onclick={()=>action(async()=>{await api('/auth/logout','POST');me=null;projects=[];selected='';events=[];analytics=null;keys=[];issuedKey='';detail=null})}>Sign out</button></div></aside>
 <main class="workspace">{#if me.demo}<div class="demo-banner" role="status"><span><b>Demo</b> · read-only view of this project · expires in an hour</span><span class="demo-links"><button class="text-button" onclick={()=>action(async()=>{await api('/auth/logout','POST');me=null;projects=[];selected='';events=[];analytics=null;keys=[];register=true})}>Create your own workspace</button></span></div>{/if}<header><div><div class="eyebrow">OBSERVE / {view==='activity'?'OVERVIEW':view.toUpperCase()}</div><h1>{view==='activity'?'AI activity, understood.':view==='keys'?'Connect your tools.':'Your data, your rules.'}</h1><p class="muted">{project?project.name:'Create your first project to start collecting events.'}</p></div><span class="badge">{project?.privacy==='FULL'?'Full collection':project?.privacy==='REDACTED'?'Redacted collection':'Metadata first'}</span></header>
 {#if error}<div class="error" role="alert">{error}</div>{/if}
 {#if !me.demo}<details class="new-project" open={!projects.length}><summary>Add a project</summary><form class="inline" onsubmit={e=>{e.preventDefault();createProject()}}><label>Project name<input bind:value={newName} required maxlength="120" placeholder="My project"/></label><button class="primary" disabled={busy}>Create project</button></form></details>{/if}
 {#if project}
 {#if view==='activity'}
 <form class="filters card" onsubmit={e=>{e.preventDefault();detail=null;action(refresh)}}><label>From (UTC)<input type="date" bind:value={from} required/></label><label>To (UTC)<input type="date" bind:value={to} required/></label><label>User<input bind:value={userFilter} placeholder="Any user ID"/></label><label>Agent<input bind:value={agentFilter} placeholder="Any agent"/></label><label>Model<input bind:value={modelFilter} placeholder="Any model"/></label><button class="primary" disabled={busy}>{busy?'Loading…':'Apply filters'}</button><details class="advanced"><summary>More filters</summary><div class="filters"><label>IDE<input bind:value={ideFilter}/></label><label>Language<input bind:value={languageFilter}/></label><label>Repository<input bind:value={repositoryFilter}/></label><label>Branch<input bind:value={branchFilter}/></label><label>Event type<input bind:value={eventFilter}/></label><label>Session ID<input bind:value={sessionFilter}/></label><label>Trace ID<input bind:value={traceFilter}/></label><label>Prompt ID<input bind:value={promptFilter}/></label></div></details></form>
 {#if traceFilter||promptFilter}<div class="focus-banner"><span>Inspecting {traceFilter?'trace':'prompt'} <code>{traceFilter||promptFilter}</code></span><button onclick={()=>{traceFilter='';promptFilter='';action(refresh)}}>Clear focus</button></div>{/if}
 {#if analytics}<section class="stats">{#each [['Prompts',analytics.totals.prompts],['Reported total tokens',tokens(analytics.totals,'total_tokens')],['Traces',analytics.totals.traces],['Files changed',analytics.totals.files_changed]] as [label,value]}<article class="card"><span class="muted">{label}</span><strong>{typeof value==='string'?value:number(Number(value))}</strong></article>{/each}</section>
 <div class="two-col"><section class="card"><div class="section-title"><h2>{group==='prompt'?'Your prompts':'Usage breakdown'}</h2><label><span class="sr-only">Group by</span><select bind:value={group} onchange={()=>action(refresh)}>{#each ['prompt','trace','user','agent','model','ide','language','day'] as g}<option value={g}>{g}</option>{/each}</select></label></div><div class="table-scroll"><table><thead><tr><th>{group==='prompt'?'your prompt':group==='trace'?'trace':group}</th><th>Summary</th><th>Input</th><th>Output</th><th>Cached</th><th>Files</th><th>Cost</th></tr></thead><tbody>{#each analytics.breakdown as b (b.name)}<tr><td>{#if group==='prompt'&&b.name!=='unknown'}<button class="text-button turn" title={b.stats.prompt_text||b.name} onclick={()=>inspectPrompt(b.name)}>{b.stats.prompt_text||'Prompt content not collected'}</button><small><span class="who" class:known={!!(b.stats.prompt_email||b.stats.prompt_ip)} title={[b.stats.prompt_email,b.stats.prompt_ip,b.stats.prompt_host].filter(Boolean).join('\n')||'No identity recorded'}>{String(b.stats.prompt_user||'User unknown')}</span> · {String(b.stats.prompt_ide||'IDE unknown')} · {String(b.stats.prompt_agent||'Agent unknown')}</small>{#if b.stats.models?.length}<span class="badge">{b.stats.models.join(', ')}</span>{/if}{:else if group==='trace'&&b.name!=='unknown'}<button class="text-button turn" onclick={()=>inspectTrace(b.name)}>{b.stats.prompt_text||b.name}</button>{:else if group==='user'&&b.name!=='unknown'}<button class="text-button" onclick={()=>{userFilter=b.name;action(refresh)}}>{b.name}</button>{:else}{b.name}{/if}</td><td class="summary">{#if b.stats.prompt_summary}<button class="text-button shape" title={b.stats.prompt_summary} onclick={()=>openSummary(String(b.stats.prompt_text||b.name),String(b.stats.prompt_summary))}>{digest(b.stats)}<span class="more">read</span></button>{:else}<span class="muted shape">{digest(b.stats)}</span>{/if}</td><td>{tokens(b.stats,'input_tokens')}</td><td>{tokens(b.stats,'output_tokens')}</td><td>{tokens(b.stats,'cached_tokens')}</td><td class="filecell">{#if b.stats.file_changes?.length}<button class="text-button filecount" onclick={()=>openChanges(String(b.stats.prompt_text||b.name),b.stats.file_changes)}>{number(b.stats.files_changed)}</button>{:else}{number(b.stats.files_changed)}{/if}{#if b.stats.lines_added||b.stats.lines_removed}<small class="delta"><span class="added">+{number(Number(b.stats.lines_added))}</span> <span class="removed">−{number(Number(b.stats.lines_removed))}</span></small>{/if}</td><td>{#each b.costs||[] as cost}<div>{cost.currency} {cost.amount} <small>{cost.type}</small></div>{:else}Not reported{/each}</td></tr>{:else}<tr><td colspan="7" class="muted">No user prompts recorded in this period.</td></tr>{/each}</tbody></table></div><p class="caption">Up to 100 groups. Prompt rows require a recorded user submission. Partial counts cover only requests reporting that category.</p></section>
 <section class="card"><h2>Recorded cost</h2>{#each analytics.costs as cost}<div class="cost"><strong>{cost.currency} {cost.amount}</strong><span class="badge">{cost.type}</span></div>{:else}<p class="muted">No costs reported. Configure model pricing or send provider charges with events.</p>{/each}<div class="coverage"><span>Usage coverage</span><strong>{analytics.totals.requests_with_total} / {analytics.totals.requests} requests include totals</strong><span>Cost coverage</span><strong>{analytics.totals.requests_with_cost} / {analytics.totals.requests} requests include costs</strong></div><p class="caption">Currencies are kept separate. Usage is not a developer performance score.</p></section></div>{/if}
 {#if promptFilter && analytics}<section class="card prompt-detail"><h2>Your prompt</h2><p class="prompt-content">{analytics.totals.prompt_text||'Prompt content not collected'}</p><p>{String(analytics.totals.prompt_user||'User unknown')} · {String(analytics.totals.prompt_ide||'IDE unknown')} · {String(analytics.totals.prompt_agent||'Agent unknown')}</p><p>Models: {analytics.totals.models?.join(', ')||'Not reported'}</p><div class="table-scroll"><table><thead><tr>{#each tokenFields as [key,label]}<th>{label}</th>{/each}</tr></thead><tbody><tr>{#each tokenFields as [key,label]}<td>{tokens(analytics.totals,key)}</td>{/each}</tr></tbody></table></div><p class="caption">Cache and reasoning categories may be included in input or output. They are not added again to the total. Partial values include only reported usage.</p></section>{/if}
 <details class="internal-activity"><summary>{promptFilter||traceFilter?'Expand internal activity':'Explore raw activity (includes unassigned events)'}</summary>
 <section class="card timeline"><div class="section-title"><div><h2>{traceFilter?'Trace timeline':promptFilter?'Prompt activity':'Activity timeline'}</h2><p class="muted">Chronological events, linked by the IDs your integration provides.</p></div><span class="badge">{events.length} loaded</span></div>{#each events as event (event.event_id)}<article class="event"><div class="dot"></div><time>{new Date(event.timestamp).toLocaleString()}</time><div class="event-main"><button class="event-title" disabled={busy} onclick={()=>detail=event}>{event.event_type}</button><p>{event.file?.path||event.prompt||event.agent?.name||event.user_id||'Metadata event'}{#if event.file&&(event.file.lines_added||event.file.lines_removed)} <span class="added">+{number(event.file.lines_added)}</span> <span class="removed">−{number(event.file.lines_removed)}</span>{/if}</p>{#if event.usage}<p class="caption">{event.model?.name||'Model unknown'} — {tokenFields.map(([key,label])=>label+': '+(event.usage?.[key]==null?'Not reported':number(event.usage[key]))).join(' · ')}</p>{/if}<div class="event-links">{#if event.trace_id}<button class="text-button" onclick={()=>inspectTrace(event.trace_id!)}>View trace ↗</button>{/if}{#if event.prompt_id}<button class="text-button" onclick={()=>inspectPrompt(event.prompt_id!)}>View prompt ↗</button>{/if}</div></div>{#if event.file}<span class="badge">{event.file.change_source||'UNKNOWN'}</span>{/if}</article>{:else}<div class="empty"><h3>Your next AI session starts here.</h3><p>Generate a project key and send your first event to see prompts, usage and file changes together.</p><button class="primary" onclick={()=>view='keys'}>Connect a tool →</button></div>{/each}{#if hasMore}<button disabled={busy} onclick={()=>action(async()=>{const p=query();p.set('offset',String(offset));const e=await api(`/projects/${selected}/events?${p}`);events=[...events,...e.events];hasMore=e.has_more;offset=e.next_offset})}>Load more events</button>{/if}</section>
 {#if detail}<section class="card detail"><div class="section-title"><h2>Event detail</h2><div class="actions">{#if !me.demo}<button class="danger" disabled={busy} onclick={()=>{const id=detail!.event_id;if(!confirm('Permanently delete this event? Use this to purge a prompt or diff that captured something it should not have. Totals recompute without it.'))return;action(async()=>{await api(`/projects/${selected}/events/${encodeURIComponent(id)}`,'DELETE');events=events.filter(e=>e.event_id!==id);detail=null;await refresh()})}}>Delete event</button>{/if}<button onclick={()=>detail=null}>Close</button></div></div><p class="muted">Prompt text is optional. Relationships are reported by the integration.</p>{#if detail.file}<div class="change"><h3>{detail.file.path}</h3><p class="muted">{detail.file.operation||'changed'} · <span class="added">+{number(detail.file.lines_added)}</span> <span class="removed">−{number(detail.file.lines_removed)}</span> · reported as {detail.file.change_source||'UNKNOWN'}</p>{#if detail.file.diff}<pre class="diff">{#each detail.file.diff.split('\n') as line}<span class={line.startsWith('+')&&!line.startsWith('+++')?'added':line.startsWith('-')&&!line.startsWith('---')?'removed':line.startsWith('@@')?'hunk':''}>{line}
</span>{/each}</pre>{:else}<p class="muted">No diff stored. Enable diffs on the project and in the integration to keep them.</p>{/if}</div>{/if}<details><summary>Raw event</summary><pre>{JSON.stringify(detail,null,2)}</pre></details></section>{/if}
 </details>
 {:else if view==='keys'}<section class="card"><h2>Project ingestion keys</h2><p class="muted">Keys can only send events to this project. Store them in your tool’s environment.</p><form class="inline" onsubmit={e=>{e.preventDefault();action(async()=>{const key=await api(`/projects/${selected}/keys`,'POST',{name:keyName});issuedKey=key.api_key;keys=await api(`/projects/${selected}/keys`)})}}><label>Key name<input bind:value={keyName} required maxlength="120"/></label><button class="primary" disabled={busy}>Generate key</button></form>{#if issuedKey}<div class="key-reveal"><strong>Copy this key now. It is shown only once.</strong><code>{issuedKey}</code><button onclick={()=>issuedKey=''}>Dismiss key</button></div>{/if}<div class="table-scroll"><table><thead><tr><th>Name</th><th>Status</th><th>Action</th></tr></thead><tbody>{#each keys as key}<tr><td>{key.name}</td><td>{key.revoked_at?'Revoked':'Active'}</td><td>{#if !key.revoked_at}<button disabled={busy} onclick={()=>action(async()=>{await api(`/projects/${selected}/keys/${key.id}`,'DELETE');keys=await api(`/projects/${selected}/keys`)})}>Revoke</button>{/if}</td></tr>{/each}</tbody></table></div><p class="caption">To rotate a key, generate a replacement, update your integration, then revoke the old key.</p></section><section class="card"><h2>Send your first event</h2><p>Use the project ID below with the documented HTTP API or Python SDK.</p><pre>{`POST /api/v1/events
Authorization: Bearer <YOUR_API_KEY>
Content-Type: application/json

${JSON.stringify({event_id:'evt_unique_id',event_type:'prompt.created',project_id:selected,user_id:me.id,session_id:'session_1',trace_id:'trace_1',prompt_id:'prompt_1',timestamp:new Date().toISOString()},null,2)}`}</pre><p class="caption">Keep the same event ID when retrying. The server counts it once.</p></section>
 {:else}<section class="card"><h2>Collection policy</h2><form onsubmit={e=>{e.preventDefault();action(async()=>{await api(`/projects/${selected}`,'PATCH',{privacy,track_diffs:trackDiffs});await loadProjects()})}}><label>Privacy mode<select bind:value={privacy}><option value="METADATA_ONLY">Metadata only — default</option><option value="REDACTED">Redacted prompt text</option><option value="FULL">Full prompt collection</option></select></label><p class="muted">Metadata mode discards prompts, diffs and arbitrary metadata. Redacted mode removes common credential patterns from prompt text; it cannot guarantee removal of every secret.</p><label class="checkbox"><input type="checkbox" bind:checked={trackDiffs}/>Allow diffs in full collection mode</label><p class="caption">Changes apply to new events. Previously stored events retain their original collection policy.</p><button class="primary" disabled={busy}>Save settings</button></form></section>{/if}
 {/if}
 {#if textSheet}
  <div class="sheet" role="dialog" aria-modal="true" aria-label="Turn summary">
   <button class="sheet-scrim" aria-label="Close summary" onclick={closeChanges}></button>
   <section class="sheet-body">
    <div class="section-title">
     <div><h2>What the agent reported</h2><p class="muted">{textSheet.label}</p></div>
     <button onclick={closeChanges}>Close</button>
    </div>
    <p class="caption">Recorded from the agent's own reply. rubberai does not summarise or score anything itself.</p>
    <div class="summary-body">{textSheet.body}</div>
   </section>
  </div>
 {/if}
 {#if changeSet}
  <div class="sheet" role="dialog" aria-modal="true" aria-label="File changes">
   <button class="sheet-scrim" aria-label="Close file changes" onclick={closeChanges}></button>
   <section class="sheet-body">
    <div class="section-title">
     <div><h2>Files changed</h2><p class="muted">{changeSet.label}</p></div>
     <button onclick={closeChanges}>Close</button>
    </div>
    <div class="sheet-split">
     <ul class="filelist">
      {#each changeSet.rows as f}
       <li>
        <button class:sel={changeFile?.path===f.path} onclick={()=>changeFile=f}>
         <span class="fstatus" class:untracked={f.status==='untracked'}>{f.status||f.operation||'changed'}</span>
         <span class="fpath">{f.path}</span>
         <span class="fdelta"><span class="added">+{f.lines_added||0}</span> <span class="removed">−{f.lines_removed||0}</span></span>
        </button>
       </li>
      {/each}
     </ul>
     <div class="filediff">
      {#if changeFile}
       <h3>{changeFile.path}</h3>
       <p class="muted">{changeFile.status||changeFile.operation||'changed'} · <span class="added">+{changeFile.lines_added||0}</span> <span class="removed">−{changeFile.lines_removed||0}</span></p>
       {#if changeFile.diff}
        <pre class="diff">{#each changeFile.diff.split('\n') as line}<span class={line.startsWith('+')&&!line.startsWith('+++')?'added':line.startsWith('-')&&!line.startsWith('---')?'removed':line.startsWith('@@')?'hunk':''}>{line}
</span>{/each}</pre>
       {:else}
        <p class="muted">No diff stored for this change. Untracked files have no prior version to compare against, and diffs are only kept when the project collects in full mode.</p>
       {/if}
      {/if}
     </div>
    </div>
   </section>
  </div>
 {/if}
 <footer>rubberai · Evidence over assumptions.</footer></main></div>
{/if}
