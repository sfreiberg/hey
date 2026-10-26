package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// request is what the fake notification service saw.
type request struct {
	Path        string
	ContentType string
	User, Pass  string
	Body        string
}

// fakeService redirects every request made through httpClient to a local
// server that replies with status and body, and returns a channel of the
// requests it received.
func fakeService(t *testing.T, status int, body string) <-chan request {
	t.Helper()
	reqs := make(chan request, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		u, p, _ := r.BasicAuth()
		reqs <- request{Path: r.URL.Path, ContentType: r.Header.Get("Content-Type"), User: u, Pass: p, Body: string(b)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	target, _ := url.Parse(srv.URL)
	orig := httpClient
	httpClient = &http.Client{Transport: redirect{target}}
	t.Cleanup(func() { httpClient = orig })
	return reqs
}

type redirect struct{ target *url.URL }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = r.target.Scheme
	req.URL.Host = r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

func receive(t *testing.T, reqs <-chan request) request {
	t.Helper()
	select {
	case r := <-reqs:
		return r
	default:
		t.Fatal("no request was sent")
		return request{}
	}
}

// result returns a finished Result for the command line args.
func result(t *testing.T, args ...string) *Result {
	t.Helper()
	start := time.Date(2024, 3, 19, 14, 5, 6, 0, time.UTC)
	cmd := exec.Command(args[0], args[1:]...)
	err := cmd.Run()
	return &Result{cmd: cmd, Start: start, End: start.Add(90 * time.Second), Err: err}
}

func TestRun(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		command string
		success bool
	}{
		{name: "no command", args: []string{"hey"}, command: "", success: true},
		{name: "success", args: []string{"hey", "true"}, command: "true", success: true},
		{name: "failure", args: []string{"hey", "false"}, command: "false", success: false},
		{name: "with args", args: []string{"hey", "sh", "-c", "exit 0"}, command: "sh -c exit 0", success: true},
		{name: "exit code", args: []string{"hey", "sh", "-c", "exit 3"}, command: "sh -c exit 3", success: false},
		{name: "not found", args: []string{"hey", "/nonexistent/command"}, command: "/nonexistent/command", success: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := run(tt.args)
			if got := res.Command(); got != tt.command {
				t.Errorf("Command() = %q; want %q", got, tt.command)
			}
			if got := res.Success(); got != tt.success {
				t.Errorf("Success() = %v; want %v (err: %v)", got, tt.success, res.Err)
			}
			if res.End.Before(res.Start) || res.Duration() < 0 {
				t.Errorf("bad timing: start %v end %v", res.Start, res.End)
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	c, err := loadConfig("hey.conf.example")
	if err != nil {
		t.Fatal(err)
	}
	if c.Plivo == nil || c.Plivo.To != "18885551234" {
		t.Errorf("plivo section not loaded: %+v", c.Plivo)
	}
	if c.Twilio == nil || c.Twilio.From != "+18885551234" {
		t.Errorf("twilio section not loaded: %+v", c.Twilio)
	}
	if c.Slack == nil || !strings.HasPrefix(c.Slack.URL, "https://hooks.slack.com/") {
		t.Errorf("slack section not loaded: %+v", c.Slack)
	}
	if n := len(c.Senders()); n != 3 {
		t.Errorf("got %d senders; want 3", n)
	}

	if _, err := loadConfig("does-not-exist.toml"); err == nil {
		t.Error("expected error for missing config file")
	}
}

func TestSenders(t *testing.T) {
	if n := len((&Config{}).Senders()); n != 0 {
		t.Errorf("empty config: got %d senders", n)
	}
	s := &Slack{}
	senders := (&Config{Slack: s}).Senders()
	if len(senders) != 1 || senders[0] != Sender(s) {
		t.Errorf("slack-only config: got %v", senders)
	}
}

func TestSlackSend(t *testing.T) {
	reqs := fakeService(t, 200, "ok")

	if err := (&Slack{URL: "https://hooks.slack.com/services/x"}).Send(result(t, "true")); err != nil {
		t.Fatal(err)
	}
	r := receive(t, reqs)
	if r.Path != "/services/x" || r.ContentType != "application/json" {
		t.Errorf("unexpected request: %+v", r)
	}
	var p struct{ Text, Username string }
	if err := json.Unmarshal([]byte(r.Body), &p); err != nil {
		t.Fatal(err)
	}
	want := ":thumbsup: `true` succeeded at 2024/3/19 14:06:36PM in 1m30s"
	if p.Text != want || p.Username != "Hey!" {
		t.Errorf("payload = %+v\nwant text %q", p, want)
	}

	if err := (&Slack{URL: "https://hooks.slack.com/x"}).Send(result(t, "false")); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(receive(t, reqs).Body), &p)
	if !strings.HasPrefix(p.Text, ":thumbsdown: `false` failed") {
		t.Errorf("failure text = %q", p.Text)
	}
}

func TestSlackCustomTemplate(t *testing.T) {
	reqs := fakeService(t, 200, "ok")
	s := &Slack{URL: "https://hooks.slack.com/x", Template: "{{ result.Command()|upper }} took {{ result.Duration().String() }}"}
	if err := s.Send(result(t, "true")); err != nil {
		t.Fatal(err)
	}
	var p struct{ Text string }
	json.Unmarshal([]byte(receive(t, reqs).Body), &p)
	if p.Text != "TRUE took 1m30s" {
		t.Errorf("text = %q", p.Text)
	}
}

func TestTwilioSend(t *testing.T) {
	reqs := fakeService(t, http.StatusCreated, `{"sid": "SM123", "status": "queued"}`)
	tw := &Twilio{AccountSid: "AC1", AuthToken: "secret", To: "+15550001", From: "+15550002"}
	if err := tw.Send(result(t, "true")); err != nil {
		t.Fatal(err)
	}
	r := receive(t, reqs)
	if r.Path != "/2010-04-01/Accounts/AC1/Messages.json" || r.User != "AC1" || r.Pass != "secret" {
		t.Errorf("unexpected request: %+v", r)
	}
	form, err := url.ParseQuery(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if form.Get("To") != "+15550001" || form.Get("From") != "+15550002" {
		t.Errorf("unexpected form: %v", form)
	}
	if want := "Finished `true` at 2024/3/19 14:06:36PM in 1m30s"; form.Get("Body") != want {
		t.Errorf("Body = %q; want %q", form.Get("Body"), want)
	}
}

func TestPlivoSend(t *testing.T) {
	reqs := fakeService(t, http.StatusAccepted, `{"message": "message(s) queued", "message_uuid": ["u1"], "api_id": "a1"}`)
	p := &Plivo{AuthId: "MA1", AuthToken: "secret", To: "15550001", From: "15550002"}
	if err := p.Send(result(t, "true")); err != nil {
		t.Fatal(err)
	}
	r := receive(t, reqs)
	if r.Path != "/v1/Account/MA1/Message/" || r.User != "MA1" || r.Pass != "secret" {
		t.Errorf("unexpected request: %+v", r)
	}
	var m struct{ Src, Dst, Text string }
	if err := json.Unmarshal([]byte(r.Body), &m); err != nil {
		t.Fatal(err)
	}
	if m.Src != "15550002" || m.Dst != "15550001" || !strings.HasPrefix(m.Text, "Finished `true`") {
		t.Errorf("unexpected message: %+v", m)
	}
}
