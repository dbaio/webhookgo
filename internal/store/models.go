package store

import (
	"encoding/base64"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dbaio/webhookgo/internal/signature"
)

// Header is a single HTTP header the URL adds to its responses.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Token is a webhook URL and the response it gives to callers.
type Token struct {
	ID                 string            `json:"uuid"`
	Description        string            `json:"description"`
	DefaultStatus      int               `json:"default_status"`
	DefaultContent     string            `json:"default_content"`
	DefaultContentType string            `json:"default_content_type"`
	ResponseHeaders    []Header          `json:"response_headers"`
	Timeout            int               `json:"timeout"`
	CORS               bool              `json:"cors"`
	Signature          *signature.Config `json:"signature,omitempty"` // checks every request; the API never sends its secret back
	IP                 string            `json:"ip"`
	UserAgent          string            `json:"user_agent"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

// Record is what is kept about a URL for abuse investigations: it outlives
// the URL by the retention period.
type Record struct {
	ID               string     `json:"uuid"`
	Status           string     `json:"status"` // live, expired or deleted
	CreatedAt        *time.Time `json:"created_at,omitempty"`
	IP               string     `json:"ip,omitempty"` // of the creator
	UserAgent        string     `json:"user_agent,omitempty"`
	Requests         int64      `json:"requests"` // received while records were kept
	FirstRequestAt   *time.Time `json:"first_request_at,omitempty"`
	LastRequestAt    *time.Time `json:"last_request_at,omitempty"`
	DeletedAt        *time.Time `json:"deleted_at,omitempty"`
	DeletedIP        string     `json:"deleted_ip,omitempty"`
	DeletedUserAgent string     `json:"deleted_user_agent,omitempty"`
	KeptUntil        *time.Time `json:"kept_until,omitempty"` // when the record expires, unless the URL is used again
}

// KV is an ordered name/value pair (query strings and form fields keep their
// original order and repeated names).
type KV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// File describes an uploaded multipart file. Its bytes stay in the raw body.
type File struct {
	Field       string `json:"field"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

// Request is a captured HTTP request.
type Request struct {
	ID             string              `json:"uuid"`
	TokenID        string              `json:"token_id"`
	Method         string              `json:"method"`
	URL            string              `json:"url"`
	Path           string              `json:"path"`
	Hostname       string              `json:"hostname"`
	IP             string              `json:"ip"`
	UserAgent      string              `json:"user_agent"`
	Protocol       string              `json:"protocol"`
	Headers        map[string][]string `json:"headers"`
	Query          []KV                `json:"query"`
	Form           []KV                `json:"form"`
	Files          []File              `json:"files"`
	ContentType    string              `json:"content_type"`
	Content        string              `json:"content"`
	Encoding       string              `json:"encoding,omitempty"` // "base64" when the body is not valid UTF-8
	Size           int64               `json:"size"`               // size of the body as received
	Truncated      bool                `json:"truncated"`          // only the first MAX_BODY_SIZE bytes were kept
	ResponseStatus int                 `json:"response_status"`
	Signature      *signature.Result   `json:"signature,omitempty"`
	CreatedAt      time.Time           `json:"created_at"`
}

// Summary is the lightweight form of a request used by lists and live events.
type Summary struct {
	ID             string           `json:"uuid"`
	Method         string           `json:"method"`
	Path           string           `json:"path"`
	Query          string           `json:"query_string"`
	IP             string           `json:"ip"`
	ContentType    string           `json:"content_type"`
	Size           int64            `json:"size"`
	ResponseStatus int              `json:"response_status"`
	Signature      signature.Status `json:"signature,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
}

// summaryField caps the path and query copied into summaries, so lists stay
// small however long a captured URL is.
const summaryField = 1024

func (r *Request) Summary() Summary {
	var qs string
	if i := strings.IndexByte(r.URL, '?'); i >= 0 {
		qs = r.URL[i+1:]
	}
	return Summary{
		ID:             r.ID,
		Method:         r.Method,
		Path:           clip(r.Path, summaryField),
		Query:          clip(qs, summaryField),
		IP:             r.IP,
		ContentType:    clip(r.ContentType, summaryField),
		Size:           r.Size,
		ResponseStatus: r.ResponseStatus,
		Signature:      sigStatus(r.Signature),
		CreatedAt:      r.CreatedAt,
	}
}

func sigStatus(r *signature.Result) signature.Status {
	if r == nil {
		return ""
	}
	return r.Status
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// SetBody fills Content and Encoding from the raw body: text when it is
// UTF-8, base64 otherwise.
func (r *Request) SetBody(b []byte) {
	if utf8.Valid(b) {
		r.Content, r.Encoding = string(b), ""
		return
	}
	if r.Truncated {
		// The MAX_BODY_SIZE cut may have split a multi-byte character.
		for i := 1; i < utf8.UTFMax && i < len(b); i++ {
			if utf8.Valid(b[:len(b)-i]) {
				r.Content, r.Encoding = string(b[:len(b)-i]), ""
				return
			}
		}
	}
	r.Content, r.Encoding = base64.StdEncoding.EncodeToString(b), "base64"
}
