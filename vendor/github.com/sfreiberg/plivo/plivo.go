// A client library for Plivo (www.plivo.com) that can be used to send text
// messages and make calls. The API is subject to change.
package plivo

import (
	"bytes"
	"encoding/json"
	"net/http"
)

const BaseURL = "https://api.plivo.com/v1/"

type Response struct {
	Msg     string   `json:"message"`
	MsgUUID []string `json:"message_uuid"`
	ApiID   string   `json:"api_id"`
	Error   string   `json:"error"`
	ReqUUID string   `json:"request_uuid"`
}

// The Message type is used for sending text messages via the JSON API.
//
// Documentation is at:
// https://www.plivo.com/docs/api/message/#send-a-message
type Message struct {
	Src    string `json:"src"`              // Mandatory
	Dst    string `json:"dst"`              // Mandatory
	Text   string `json:"text"`             // Mandatory
	Type   string `json:"type,omitempty"`   // Optional
	URL    string `json:"url,omitempty"`    // Optional
	Method string `json:"method,omitempty"` // Optional, Plivo defaults to POST
	Log    bool   `json:"log,omitempty"`    // Optional, Plivo defaults to true
}

// https://www.plivo.com/docs/api/call/#make-an-outbound-call
type Call struct {
	From                   string `json:"from"`                                // Mandatory
	To                     string `json:"to"`                                  // Mandatory
	AnswerURL              string `json:"answer_url"`                          // Mandatory
	AnswerMethod           string `json:"answer_method,omitempty"`             // Optional, Plivo defaults to POST
	RingURL                string `json:"ring_url,omitempty"`                  // Optional
	RingMethod             string `json:"ring_method,omitempty"`               // Optional, Plivo defaults to POST
	HangupURL              string `json:"hangup_url,omitempty"`                // Optional
	HangupMethod           string `json:"hangup_method,omitempty"`             // Optional, Plivo defaults to POST
	FallbackURL            string `json:"fallback_url,omitempty"`              // Optional
	FallbackMethod         string `json:"fallback_method,omitempty"`           // Optional, Plivo defaults to POST
	CallerName             string `json:"caller_name,omitempty"`               // Optional
	SendDigits             string `json:"send_digits,omitempty"`               // Optional
	SendOnPreanswer        bool   `json:"send_on_preanswer,omitempty"`         // Optional, Plive defaults to false
	TimeLimit              uint   `json:"time_limit,omitempty"`                // Optional
	HangupOnRing           uint   `json:"hangup_on_ring,omitempty"`            // Optional
	MachineDetection       string `json:"machine_detection,omitempty"`         // Optional, valid values are true and hangup
	MachineDetectionTime   uint   `json:"machine_detection_time,omitempty"`    // Optional, should be >= 2000 and <= 10000 (ms)
	MachineDetectionURL    string `json:"machine_detection_url,omitempty"`     // Optional
	MachineDetectionMethod string `json:"machine_detection_method,omitempty"`  // Optional, Plivo defaults to POST
	SIPHeaders             string `json:"sip_headers,omitempty"`               // Optional
	RingTimeout            string `json:"ring_timeout,omitempty"`              // Optional
	ParentCallUUID         string `json:"parent_call_uuid,omitempty"`          // Optional
	ErrorIfParentNotFound  bool   `json:"error_if_parent_not_found,omitempty"` // Optional
}

type Client struct {
	AuthID     string
	AuthToken  string
	HTTPClient *http.Client
}

func (c *Client) Call(call *Call) (*Response, error) {
	url := BaseURL + "Account/" + c.AuthID + "/Call/"

	return c.send(url, "POST", call)
}

func (c *Client) Message(m *Message) (*Response, error) {
	url := BaseURL + "Account/" + c.AuthID + "/Message/"

	return c.send(url, "POST", m)
}

func (c *Client) send(url, method string, i interface{}) (*Response, error) {
	res := &Response{}

	body, err := json.Marshal(i)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(c.AuthID, c.AuthToken)
	req.Header.Set("Content-Type", "application/json")

	if c.HTTPClient == nil {
		c.HTTPClient = http.DefaultClient
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	err = json.NewDecoder(resp.Body).Decode(res)

	return res, err
}
