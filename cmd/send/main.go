// rubberai-send connects shell hooks, CI scripts and custom tools to the same API.
package main

import (
 "bytes"
 "crypto/rand"
 "encoding/hex"
 "encoding/json"
 "flag"
 "fmt"
 "io"
 "net/http"
 "net/url"
 "os"
 "strings"
 "time"

 "rubberai/internal/event"
)
func run() error {
 endpoint:=flag.String("url",os.Getenv("RUBBERAI_URL"),"server or collector connection URL")
 project:=flag.String("project",os.Getenv("RUBBERAI_PROJECT_ID"),"project ID")
 keyEnv:=flag.String("key-env","RUBBERAI_API_KEY","environment variable containing project key or collector local token")
 agent:=flag.String("agent","","agent name (metadata only)")
 ide:=flag.String("ide","","IDE or terminal name (metadata only)")
 user:=flag.String("user",os.Getenv("RUBBERAI_USER_ID"),"external user ID")
 status:=flag.Bool("status",false,"read collector queue status instead of sending")
 flag.Parse()
 u,err:=url.Parse(*endpoint);if err!=nil||u.Host==""||(u.Scheme!="https"&&u.Scheme!="http"){return fmt.Errorf("set a valid --url or RUBBERAI_URL")}
 if u.Scheme=="http"&&u.Hostname()!="localhost"&&u.Hostname()!="127.0.0.1"&&u.Hostname()!="::1"{return fmt.Errorf("remote endpoints require HTTPS")}
 key:=os.Getenv(*keyEnv);if key==""{return fmt.Errorf("credential environment variable is empty")}
 method,path:="POST","/api/v1/events";var body []byte
 if *status {method,path="GET","/api/v1/status"} else {
  raw,err:=io.ReadAll(io.LimitReader(os.Stdin,(1<<20)+1));if err!=nil||len(raw)>1<<20{return fmt.Errorf("input exceeds 1 MiB")}
  var envelope struct{ Events []event.Event `json:"events"` }
  if json.Unmarshal(raw,&envelope)!=nil{return fmt.Errorf("expected JSON event or events envelope on stdin")}
  list:=envelope.Events
  if list==nil {var e event.Event;if json.Unmarshal(raw,&e)!=nil{return fmt.Errorf("invalid event")};list=[]event.Event{e}}
  if len(list)<1||len(list)>100{return fmt.Errorf("send 1–100 events")}
  for i:=range list {
   e:=&list[i];if e.ProjectID==""{e.ProjectID=*project};if *project!=""&&e.ProjectID!=*project{return fmt.Errorf("project mismatch")}
   if e.ID=="" {b:=make([]byte,16);if _,err=rand.Read(b);err!=nil{return err};e.ID="evt_"+hex.EncodeToString(b)}
   if e.Timestamp.IsZero(){e.Timestamp=time.Now().UTC()}
   if e.UserID==""{e.UserID=*user};if e.Agent.Name==""{e.Agent.Name=*agent};if e.IDE.Name==""{e.IDE.Name=*ide}
   if err=e.Validate();err!=nil{return err}
  }
  body,err=json.Marshal(map[string]any{"events":list});if err!=nil{return err}
 }
 req,err:=http.NewRequest(method,strings.TrimRight(*endpoint,"/")+path,bytes.NewReader(body));if err!=nil{return err}
 req.Header.Set("Authorization","Bearer "+key);req.Header.Set("Content-Type","application/json")
 client:=&http.Client{Timeout:2*time.Second,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}
 res,err:=client.Do(req);if err!=nil{return fmt.Errorf("delivery failed; use the local collector and retry with the same explicit event IDs")};defer res.Body.Close()
 if res.StatusCode<200||res.StatusCode>=300{return fmt.Errorf("endpoint returned HTTP %d",res.StatusCode)}
 _,err=io.Copy(os.Stdout,io.LimitReader(res.Body,4096));return err
}
func main(){if err:=run();err!=nil{fmt.Fprintln(os.Stderr,"rubberai-send:",err);os.Exit(1)}}
