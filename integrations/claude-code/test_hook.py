import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec=importlib.util.spec_from_file_location('hook',Path(__file__).with_name('hook.py'))
h=importlib.util.module_from_spec(spec);spec.loader.exec_module(h)

class HookTest(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory();self.addCleanup(self.tmp.cleanup)
  self.env=patch.dict(os.environ,{'XDG_STATE_HOME':self.tmp.name});self.env.start();self.addCleanup(self.env.stop)
  self.path=Path(self.tmp.name)/'session.jsonl';self.path.write_text('')
  self.payload={'session_id':'s','transcript_path':str(self.path),'prompt':'human prompt'}
  self.cfg={'send_prompts':False};self.proj={'project_id':'p'};self.sent=[]
  self.sender=patch.object(h,'send',side_effect=lambda c,p,e:self.sent.extend(e) or True);self.sender.start();self.addCleanup(self.sender.stop)
 def record(self,id,usage):
  with self.path.open('a') as f:f.write(json.dumps({'type':'assistant','uuid':id,'message':{'model':'test','usage':usage}})+'\n')
 def test_three_prompts_and_token_subsets(self):
  self.record('old',{'input_tokens':999})
  for n in range(3):
   h.handle(self.cfg,self.proj,self.payload,'UserPromptSubmit')
   for m in range(2):self.record(f'{n}-{m}',{'input_tokens':10,'output_tokens':5,'cache_read_input_tokens':20,'cache_creation_input_tokens':3})
   h.handle(self.cfg,self.proj,self.payload,'Stop')
  prompts=[e for e in self.sent if e['event_type']=='prompt.created'];calls=[e for e in self.sent if e['event_type']=='llm.request.completed']
  self.assertEqual(len(prompts),3);self.assertEqual(len(calls),6)
  for p in prompts:self.assertEqual(sum(e.get('prompt_id')==p['prompt_id'] for e in calls),2)
  self.assertEqual(calls[0]['usage'],{'input_tokens':33,'output_tokens':5,'cached_tokens':20,'cache_write_tokens':3})
 def test_missing_counts_remain_missing(self):
  h.handle(self.cfg,self.proj,self.payload,'UserPromptSubmit');self.record('partial',{'output_tokens':5})
  h.handle(self.cfg,self.proj,self.payload,'Stop')
  self.assertEqual(self.sent[-1]['usage'],{'output_tokens':5})
 def test_retry_keeps_original_prompt(self):
  h.handle(self.cfg,self.proj,self.payload,'UserPromptSubmit');first=self.sent[-1]['prompt_id']
  self.record('first',{'output_tokens':1})
  with patch.object(h,'send',return_value=False):h.handle(self.cfg,self.proj,self.payload,'UserPromptSubmit')
  second=h.read_state('s')['prompt_id'];self.record('second',{'output_tokens':2})
  h.handle(self.cfg,self.proj,self.payload,'Stop')
  calls={e['event_id']:e for e in self.sent if e['event_type']=='llm.request.completed'}
  self.assertEqual(calls['evt_llm_first']['prompt_id'],first);self.assertEqual(calls['evt_llm_second']['prompt_id'],second)
 def test_response_fragments_count_once(self):
  h.handle(self.cfg,self.proj,self.payload,'UserPromptSubmit')
  for n in [1,5]:
   with self.path.open('a') as f:
    f.write(json.dumps({'type':'assistant','uuid':str(n),'message':{'id':'same-response','model':'test','usage':{'output_tokens':n}}})+'\n')
  h.handle(self.cfg,self.proj,self.payload,'Stop')
  calls=[e for e in self.sent if e['event_type']=='llm.request.completed']
  self.assertEqual(len(calls),1);self.assertEqual(calls[0]['usage']['output_tokens'],5)
 def test_reported_zero_is_preserved(self):
  h.handle(self.cfg,self.proj,self.payload,'UserPromptSubmit');self.record('zero',{'input_tokens':0,'output_tokens':0,'cache_read_input_tokens':0})
  h.handle(self.cfg,self.proj,self.payload,'Stop')
  self.assertEqual(self.sent[-1]['usage']['cached_tokens'],0)
 def test_failed_prompt_retries(self):
  with patch.object(h,'send',return_value=False):h.handle(self.cfg,self.proj,self.payload,'UserPromptSubmit')
  pid=h.read_state('s')['prompt_id']
  h.handle(self.cfg,self.proj,self.payload,'Stop')
  self.assertEqual(self.sent[0]['prompt_id'],pid)
  self.assertNotIn('pending_prompts',h.read_state('s'))
 def test_missing_state_does_not_replay(self):
  self.record('old',{'output_tokens':3});h.handle(self.cfg,self.proj,self.payload,'Stop');self.assertEqual(self.sent,[])

if __name__=='__main__':unittest.main()
