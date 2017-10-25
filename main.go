package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/sfreiberg/gotwilio"
	"github.com/sfreiberg/plivo"
	"gopkg.in/flosch/pongo2.v3"
)

var config *Config

type Sender interface {
	Send(res *Result) error
}

type Result struct {
	*exec.Cmd
	Start time.Time
	End   time.Time
	Err   error
}

func (r *Result) Duration() time.Duration {
	return r.End.Sub(r.Start)
}

// Returns the command with arguments
func (r *Result) Command() string {
	return strings.Join(r.Cmd.Args, " ")
}

type Config struct {
	*Plivo  `toml:"plivo,omitempty"`
	*Twilio `toml:"twilio,omitempty"`
	*Slack  `toml:"slack,omitempty"`
}

func (c *Config) Senders() []Sender {
	senders := []Sender{}

	if c.Plivo != nil {
		senders = append(senders, c.Plivo)
	}

	if c.Twilio != nil {
		senders = append(senders, c.Twilio)
	}

	if c.Slack != nil {
		senders = append(senders, c.Slack)
	}

	return senders
}

type Slack struct {
	URL      string `toml:"url"`
	Template string `toml:"template"`
}

func (s *Slack) Send(res *Result) error {
	type Payload struct {
		Text      string `json:"text"`
		IconURL   string `json:"icon_url,omitempty"`
		IconEmoji string `json:"icon_emoji,omitempty"`
		Username  string `json:"username,omitempty"`
	}

	var tmpl string
	payload := &Payload{Username: "Hey!"}

	if s.Template == "" {
		tmpl = "{% if result.Cmd.ProcessState.Success() %}:thumbsup:{% else %}:thumbsdown:{% endif %} Finished `{{ result.Command() }}` at {{ result.End|time:\"2006/1/_2 15:04:05PM\" }} in {{ result.Duration().String() }}"
	} else {
		tmpl = s.Template
	}

	payload.Text = pongo2.RenderTemplateString(tmpl, pongo2.Context{"result": res})

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", s.URL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	return nil
}

type Twilio struct {
	AccountSid string `toml:"account_sid"`
	AuthToken  string `toml:"auth_token"`
	To         string `toml:"to"`
	From       string `toml:"from"`
	Template   string `toml:"template"`
}

func (t *Twilio) Send(res *Result) error {
	var tmpl string

	if t.Template == "" {
		tmpl = "Finished `{{ result.Command()|truncatechars:76 }}` at {{ result.End|time:\"2006/1/_2 15:04:05PM\" }} in {{ result.Duration().String() }}"
	} else {
		tmpl = t.Template
	}

	msg := pongo2.RenderTemplateString(tmpl, pongo2.Context{"result": res})

	twilio := gotwilio.NewTwilioClient(t.AccountSid, t.AuthToken)
	_, _, err := twilio.SendSMS(t.From, t.To, msg, "", "")
	return err
}

type Plivo struct {
	AuthId    string `toml:"auth_id"`
	AuthToken string `toml:"auth_token"`
	To        string `toml:"to"`
	From      string `toml:"from"`
	Template  string `toml:"template"`
}

func (p *Plivo) Send(res *Result) error {
	var tmpl string

	if p.Template == "" {
		tmpl = "Finished `{{ result.Command()|truncatechars:76 }}` at {{ result.End|time:\"2006/1/_2 15:04:05PM\" }} in {{ result.Duration().String() }}"
	} else {
		tmpl = p.Template
	}

	msg := pongo2.RenderTemplateString(tmpl, pongo2.Context{"result": res})

	m := &plivo.Message{
		Src:  p.From,
		Dst:  p.To,
		Text: msg,
	}

	client := &plivo.Client{AuthID: p.AuthId, AuthToken: p.AuthToken}
	_, err := client.Message(m) // TODO: Determine if the response is useful

	return err
}

func init() {
	var err error
	config, err = loadConfig()
	if err != nil {
		log.Fatalf("Error loading config: %s\n", err)
	}
}

func main() {
	res := run()

	errs := []error{}

	for _, sender := range config.Senders() {
		err := sender.Send(res)
		if err != nil { // TODO: Add the sender name
			errs = append(errs, err)
		}
	}

	// TODO: Return the exit code of the calling program if no errors were
	// reported.
	for _, e := range errs {
		log.Printf("Error sending notification: %s\n", e)
	}

	// TODO: Try to preserve the exit code if possible. Need to find a cross
	// platform way to handle it. For now I'm taking the easy road.
	if len(errs) > 0 || !res.Cmd.ProcessState.Success() {
		os.Exit(1)
	}
}

func run() *Result {
	res := &Result{Start: time.Now()}

	if len(os.Args) == 1 {
		res.Err = errors.New("You must suplly a command to run")
		return res
	}

	if len(os.Args) == 2 {
		res.Cmd = exec.Command(os.Args[1])
	} else {
		res.Cmd = exec.Command(os.Args[1], os.Args[2:]...)
	}

	res.Cmd.Stdin = os.Stdin
	res.Cmd.Stdout = os.Stdout
	res.Cmd.Stderr = os.Stderr
	res.Err = res.Cmd.Run()
	res.End = time.Now()

	return res
}

// TODO: Currently loads from $HOME/.hey.toml. Add the ability to fallback to
// the current working directory if that fails.
func loadConfig() (*Config, error) {
	c := &Config{}

	u, err := user.Current()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(u.HomeDir, ".hey.toml")

	_, err = toml.DecodeFile(path, c)

	return c, err
}
