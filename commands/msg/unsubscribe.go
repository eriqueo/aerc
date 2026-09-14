package msg

import (
	"bufio"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"git.sr.ht/~rjarry/aerc/app"
	"git.sr.ht/~rjarry/aerc/commands"
	"git.sr.ht/~rjarry/aerc/config"
	"git.sr.ht/~rjarry/aerc/lib"
	"git.sr.ht/~rjarry/aerc/lib/auth"
	"git.sr.ht/~rjarry/aerc/lib/log"
	"github.com/emersion/go-message/mail"
)

const (
	choiceHTTPS = "HTTPS (recommended)"
	choiceEmail = "Email (fallback)"
	choiceHTTP  = "Website (not secure)"
	oneClickArg = "List-Unsubscribe=One-Click"
)

type unsubscribeChoice struct {
	label  string
	method *url.URL
}

// Unsubscribe helps people unsubscribe from mailing lists by way of the
// List-Unsubscribe header.
type Unsubscribe struct {
	Edit       bool `opt:"-e" desc:"Force [compose].edit-headers = true."`
	NoEdit     bool `opt:"-E" desc:"Force [compose].edit-headers = false."`
	SkipEditor bool `opt:"-s" desc:"Skip the editor and go directly to the review screen."`
}

func init() {
	commands.Register(Unsubscribe{})
}

func (Unsubscribe) Description() string {
	return "Attempt to automatically unsubscribe from mailing lists."
}

func (Unsubscribe) Context() commands.CommandContext {
	return commands.MESSAGE_LIST | commands.MESSAGE_VIEWER
}

// Aliases returns a list of aliases for the :unsubscribe command
func (Unsubscribe) Aliases() []string {
	return []string{"unsubscribe"}
}

// Execute runs the Unsubscribe command
func (u Unsubscribe) Execute(args []string) error {
	editHeaders := (config.Compose.EditHeaders || u.Edit) && !u.NoEdit

	widget := app.SelectedTabContent().(app.ProvidesMessage)
	msg, err := widget.SelectedMessage()
	if err != nil {
		return err
	}

	headers := msg.RFC822Headers

	details, err := auth.CreateParser(auth.DKIM)(headers, widget.SelectedAccount().AccountConfig().TrustedAuthRes)
	switch {
	case err != nil:
		return errors.New("Failed to validate DKIM signature")
	case slices.Contains(details.Results, auth.ResultFail):
		return errors.New("DKIM validation failed")
	case !slices.Contains(details.Results, auth.ResultPass):
		return errors.New("No passing DKIM signature found")
	}

	if !headers.Has("list-unsubscribe") {
		return errors.New("No List-Unsubscribe header found")
	}
	text, err := headers.Text("List-Unsubscribe")
	if err != nil {
		return err
	}
	methods := parseUnsubscribeMethods(text)
	if len(methods) == 0 {
		return fmt.Errorf("no methods found to unsubscribe")
	}
	log.Debugf("unsubscribe: found %d methods", len(methods))

	unsubscribe := func(method *url.URL) {
		log.Debugf("unsubscribe: trying to unsubscribe using %s", method.Scheme)
		var err error
		switch strings.ToLower(method.Scheme) {
		case "mailto":
			err = unsubscribeMailto(method, editHeaders, u.SkipEditor)
		case "http", "https":
			err = unsubscribeHTTP(method, headers.Values("List-Unsubscribe-Post"))
		default:
			err = fmt.Errorf("unsubscribe: skipping unrecognized scheme: %s", method.Scheme)
		}
		if err != nil {
			app.PushError(err.Error())
		}
	}

	title := "Choose unsubscribe method"
	if msg != nil && msg.Envelope != nil && len(msg.Envelope.From) > 0 {
		sender := msg.Envelope.From[0].Name
		if sender == "" {
			sender = msg.Envelope.From[0].Address
		}
		title = fmt.Sprintf("Unsubscribe from %s", sender)
	}

	choices := unsubscribeChoices(methods)
	options := make([]string, len(choices))
	for i, choice := range choices {
		options[i] = choice.label
	}

	if len(choices) == 1 {
		unsubscribe(choices[0].method)
		return nil
	}

	dialog := app.NewSelectorDialog(
		title,
		"HTTPS removes you directly. Email opens a draft if HTTPS fails.",
		options, 0, app.SelectedAccountUiConfig(),
		func(option string, err error) {
			app.CloseDialog()
			if err != nil {
				if errors.Is(err, app.ErrNoOptionSelected) {
					app.PushStatus("Unsubscribe: "+err.Error(),
						5*time.Second)
				} else {
					app.PushError("Unsubscribe: " + err.Error())
				}
				return
			}
			for _, choice := range choices {
				if choice.label == option {
					unsubscribe(choice.method)
					return
				}
			}
			app.PushError("Unsubscribe: selected method not found")
		},
	)
	app.AddDialog(dialog)

	return nil
}

func unsubscribeChoices(methods []*url.URL) []unsubscribeChoice {
	choices := make([]unsubscribeChoice, 0, len(methods))
	appendScheme := func(scheme string) {
		for _, method := range methods {
			if strings.EqualFold(method.Scheme, scheme) {
				choices = append(choices, unsubscribeChoice{method: method})
			}
		}
	}

	// HTTPS is the authenticated one-click route defined by RFC 8058. Keep it
	// first even when a sender puts mailto first in the header.
	appendScheme("https")
	appendScheme("mailto")
	appendScheme("http")
	for _, method := range methods {
		scheme := strings.ToLower(method.Scheme)
		if scheme != "https" && scheme != "mailto" && scheme != "http" {
			choices = append(choices, unsubscribeChoice{method: method})
		}
	}

	labelCounts := make(map[string]int)
	for i := range choices {
		var label string
		switch strings.ToLower(choices[i].method.Scheme) {
		case "https":
			label = choiceHTTPS
		case "mailto":
			label = choiceEmail
		case "http":
			label = choiceHTTP
		default:
			label = strings.ToUpper(choices[i].method.Scheme)
		}
		labelCounts[label]++
		if labelCounts[label] > 1 {
			label = fmt.Sprintf("%s %d", label, labelCounts[label])
		}
		choices[i].label = label
	}
	return choices
}

// parseUnsubscribeMethods reads the list-unsubscribe header and parses it as a
// list of angle-bracket <> deliminated URLs. See RFC 2369.
func parseUnsubscribeMethods(header string) (methods []*url.URL) {
	r := bufio.NewReader(strings.NewReader(header))
	for {
		// discard until <
		_, err := r.ReadSlice('<')
		if err != nil {
			return
		}
		// read until <
		m, err := r.ReadSlice('>')
		if err != nil {
			return
		}
		m = m[:len(m)-1]
		if u, err := url.Parse(string(m)); err == nil {
			methods = append(methods, u)
		}
	}
}

func unsubscribeMailto(u *url.URL, editHeaders, skipEditor bool) error {
	widget := app.SelectedTabContent().(app.ProvidesMessage)
	acct := widget.SelectedAccount()
	if acct == nil {
		return errors.New("No account selected")
	}

	h := &mail.Header{}
	h.SetSubject(u.Query().Get("subject"))
	if to, err := mail.ParseAddressList(u.Opaque); err == nil {
		h.SetAddressList("to", to)
	}

	composer, err := app.NewComposer(
		acct,
		acct.AccountConfig(),
		acct.Worker(),
		editHeaders,
		"",
		h,
		nil,
		strings.NewReader(u.Query().Get("body")),
	)
	if err != nil {
		return err
	}
	composer.Tab = app.NewTab(composer, "unsubscribe")
	if skipEditor {
		composer.Terminal().Close()
	} else {
		composer.FocusTerminal()
	}
	return nil
}

func unsubscribeHTTP(u *url.URL, postData []string) error {
	body, oneClick := oneClickPostBody(postData)
	canPost := strings.EqualFold(u.Scheme, "https") && oneClick
	options := []string{"Cancel", "Open website"}
	focus := 1
	prompt := fmt.Sprintf("This sender needs confirmation at %s.", u.Host)
	if canPost {
		options = []string{"Cancel", "Unsubscribe now", "Open website"}
		prompt = fmt.Sprintf("Send a one-click HTTPS request to %s.", u.Host)
	}

	confirm := app.NewSelectorDialog(
		"Unsubscribe?",
		prompt,
		options, focus, app.SelectedAccountUiConfig(),
		func(option string, err error) {
			app.CloseDialog()
			if err != nil || option == "Cancel" {
				return
			}
			switch option {
			case "Unsubscribe now":
				client := &http.Client{
					Timeout: 15 * time.Second,
					CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
						return http.ErrUseLastResponse
					},
				}
				go func(client *http.Client) {
					defer log.PanicHandler()
					statusCode, status, err := postOneClick(client, u, body)
					if err != nil {
						app.PushError(fmt.Sprintf("Unsubscribe request failed: %v", err))
						return
					}
					if statusCode < 200 || statusCode >= 300 {
						app.PushError(fmt.Sprintf(
							"Unsubscribe request returned %s; use Open website instead.",
							status))
						return
					}
					app.PushStatus(fmt.Sprintf(
						"Unsubscribe request accepted by %s (%s)", u.Host, status),
						10*time.Second)
				}(client)
			case "Open website":
				go func() {
					defer log.PanicHandler()
					mime := fmt.Sprintf("x-scheme-handler/%s", u.Scheme)
					if err := lib.XDGOpenMime(u.String(), mime, ""); err != nil {
						app.PushError("Unsubscribe: " + err.Error())
					}
				}()
			}
		},
	)
	app.AddDialog(confirm)
	return nil
}

func oneClickPostBody(postData []string) (string, bool) {
	for _, value := range postData {
		if strings.TrimSpace(value) == oneClickArg {
			return oneClickArg, true
		}
	}
	return "", false
}

func postOneClick(client *http.Client, endpoint *url.URL, body string) (
	statusCode int, status string, err error,
) {
	req, err := http.NewRequest(http.MethodPost, endpoint.String(), strings.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	return resp.StatusCode, resp.Status, nil
}
