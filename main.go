package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/flosch/pongo2/v6"
	"github.com/sfreiberg/gotwilio"
	"github.com/sfreiberg/plivo"
)

var config *Config

// httpClient is used for all notification requests. It is a variable so
// tests can redirect requests to a local server.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// render executes a pongo2 template with res available as "result". Unlike
// pongo2.RenderTemplateString it reports template syntax errors instead of
// panicking.
func render(tmpl string, res *Result) (string, error) {
	t, err := pongo2.FromString(tmpl)
	if err != nil {
		return "", err
	}
	return t.Execute(pongo2.Context{"result": res})
}

type Sender interface {
	Send(res *Result) error
}

type Result struct {
	cmd   *exec.Cmd
	Start time.Time
	End   time.Time
	Err   error
}

func (r *Result) Duration() time.Duration {
	return r.End.Sub(r.Start)
}

// Returns the command with arguments
func (r *Result) Command() string {
	return strings.Join(r.cmd.Args, " ")
}

func (r *Result) Success() bool {
	// If the user didn't supply a command we'll consider that a success
	if r.Command() == "" {
		return true
	}

	if r.Err != nil || r.cmd.ProcessState.Success() == false {
		return false
	}

	return true
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
		tmpl = "{% if result.Success() %}:thumbsup:{% else %}:thumbsdown:{% endif %} `{{ result.Command() }}` {% if result.Success() %}succeeded{% else %}failed{% endif %} at {{ result.End|time:\"2006/1/_2 15:04:05PM\" }} in {{ result.Duration().String() }}"
	} else {
		tmpl = s.Template
	}

	text, err := render(tmpl, res)
	if err != nil {
		return err
	}
	payload.Text = text

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", s.URL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("slack: unexpected status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

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

	msg, err := render(tmpl, res)
	if err != nil {
		return err
	}

	twilio := gotwilio.NewTwilioClientCustomHTTP(t.AccountSid, t.AuthToken, httpClient)
	_, exception, err := twilio.SendSMS(t.From, t.To, msg, "", "")
	if exception != nil {
		return fmt.Errorf("twilio: %w", *exception)
	}
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

	msg, err := render(tmpl, res)
	if err != nil {
		return err
	}

	m := &plivo.Message{
		Src:  p.From,
		Dst:  p.To,
		Text: msg,
	}

	client := &plivo.Client{AuthID: p.AuthId, AuthToken: p.AuthToken, HTTPClient: httpClient}
	resp, err := client.Message(m)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("plivo: %s", resp.Error)
	}

	return nil
}

func main() {
	path, err := defaultConfigPath()
	if err != nil {
		log.Fatalf("Error loading config: %s\n", err)
	}
	config, err = loadConfig(path)
	if err != nil {
		log.Fatalf("Error loading config: %s\n", err)
	}

	res := run(os.Args)

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
	if len(errs) > 0 || !res.Success() {
		os.Exit(1)
	}
}

// run executes args[1:] (args is os.Args) and records the result.
func run(args []string) *Result {
	res := &Result{Start: time.Now()}

	if len(args) == 1 {
		res.cmd = exec.Command("")
	} else if len(args) == 2 {
		res.cmd = exec.Command(args[1])
	} else {
		res.cmd = exec.Command(args[1], args[2:]...)
	}

	res.cmd.Stdin = os.Stdin
	res.cmd.Stdout = os.Stdout
	res.cmd.Stderr = os.Stderr
	res.Err = res.cmd.Run()
	res.End = time.Now()

	return res
}

// TODO: Currently loads from $HOME/.hey.toml. Add the ability to fallback to
// the current working directory if that fails.
func defaultConfigPath() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}

	return filepath.Join(u.HomeDir, ".hey.toml"), nil
}

func loadConfig(path string) (*Config, error) {
	c := &Config{}

	_, err := toml.DecodeFile(path, c)

	return c, err
}
