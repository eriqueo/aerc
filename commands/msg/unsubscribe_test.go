package msg

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseUnsubscribe(t *testing.T) {
	type tc struct {
		hdr      string
		expected []string
	}
	cases := []*tc{
		{"", []string{}},
		{"invalid", []string{}},
		{"<https://example.com>, <http://example.com>", []string{
			"https://example.com", "http://example.com",
		}},
		{"<https://example.com> is a URL", []string{
			"https://example.com",
		}},
		{
			"<mailto:user@host?subject=unsubscribe>, <https://example.com>",
			[]string{
				"mailto:user@host?subject=unsubscribe", "https://example.com",
			},
		},
		{"<>, <https://example> ", []string{
			"", "https://example",
		}},
	}
	for _, c := range cases {
		result := parseUnsubscribeMethods(c.hdr)
		if len(result) != len(c.expected) {
			t.Errorf("expected %d methods but got %d", len(c.expected), len(result))
			continue
		}
		for idx := 0; idx < len(result); idx++ {
			if result[idx].String() != c.expected[idx] {
				t.Errorf("expected %v but got %v", c.expected[idx], result[idx])
			}
		}
	}
}

func TestUnsubscribeChoicesPreferHTTPS(t *testing.T) {
	mailto, _ := url.Parse("mailto:list@example.com?subject=unsubscribe")
	https, _ := url.Parse("https://example.com/unsubscribe/opaque")

	choices := unsubscribeChoices([]*url.URL{mailto, https})
	if got, want := choices[0].label, choiceHTTPS; got != want {
		t.Fatalf("first choice = %q, want %q", got, want)
	}
	if got, want := choices[0].method.String(), https.String(); got != want {
		t.Fatalf("first method = %q, want %q", got, want)
	}
	if got, want := choices[1].label, choiceEmail; got != want {
		t.Fatalf("second choice = %q, want %q", got, want)
	}
}

func TestUnsubscribeFlowRequiresConfirmationForEmailOnly(t *testing.T) {
	mailto, _ := url.Parse("mailto:list@example.com?subject=unsubscribe")
	https, _ := url.Parse("https://example.com/unsubscribe/opaque")

	tests := []struct {
		name    string
		choices []unsubscribeChoice
		want    unsubscribeFlow
	}{
		{
			name: "email only",
			choices: []unsubscribeChoice{
				{label: choiceEmail, method: mailto},
			},
			want: flowConfirmEmail,
		},
		{
			name: "https only",
			choices: []unsubscribeChoice{
				{label: choiceHTTPS, method: https},
			},
			want: flowExecuteMethod,
		},
		{
			name: "multiple methods",
			choices: []unsubscribeChoice{
				{label: choiceHTTPS, method: https},
				{label: choiceEmail, method: mailto},
			},
			want: flowChooseMethod,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := unsubscribeFlowFor(test.choices); got != test.want {
				t.Fatalf("flow = %v, want %v", got, test.want)
			}
		})
	}
}

func TestOneClickPostBody(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		ok   bool
	}{
		{"missing", nil, false},
		{"wrong value", []string{"List-Unsubscribe=No"}, false},
		{"one click", []string{" List-Unsubscribe=One-Click "}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, ok := oneClickPostBody(test.in)
			if ok != test.ok {
				t.Fatalf("ok = %v, want %v", ok, test.ok)
			}
			if ok && body != oneClickArg {
				t.Fatalf("body = %q, want %q", body, oneClickArg)
			}
		})
	}
}

func TestPostOneClickSendsOneBoundedRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if got, want := r.Header.Get("Content-Type"), "application/x-www-form-urlencoded"; got != want {
			t.Errorf("content type = %q, want %q", got, want)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		if got, want := string(body), oneClickArg; got != want {
			t.Errorf("body = %q, want %q", got, want)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	endpoint, _ := url.Parse(server.URL)
	client := &http.Client{Timeout: time.Second}
	code, _, err := postOneClick(client, endpoint, oneClickArg)
	if err != nil {
		t.Fatalf("postOneClick: %v", err)
	}
	if got, want := code, http.StatusNoContent; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("request count = %d, want 1", got)
	}
}

func TestPostOneClickDoesNotFollowRedirect(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "/unexpected", http.StatusFound)
	}))
	defer server.Close()

	endpoint, _ := url.Parse(server.URL)
	client := &http.Client{
		Timeout: time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	code, _, err := postOneClick(client, endpoint, oneClickArg)
	if err != nil {
		t.Fatalf("postOneClick: %v", err)
	}
	if got, want := code, http.StatusFound; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("request count = %d, want 1", got)
	}
}
