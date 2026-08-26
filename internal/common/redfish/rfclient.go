package redfish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// maxCollectionPages caps Members@odata.nextLink pagination follows for a
// single collection fetch. Defensive only: no real DMTF-conformant BMC comes
// close to this; it exists so a malformed/adversarial nextLink cycle can't
// hang a poll/job goroutine forever (gofish's CollectList has no such cap).
const maxCollectionPages = 1000

// rfAPI is a minimal hand-written Redfish HTTP client. It replaces gofish's
// *gofish.APIClient: Basic-Auth or session-token GET/POST/PATCH against a
// BMC's Redfish service root, with JSON decoding left to callers. Method
// names/signatures mirror gofish's api.Get/api.Post/api.Patch so the small
// number of call sites in this package that issue raw requests
// (screenshot.go, sol_ssh.go) needed no logic changes, only the type they're
// built from.
type rfAPI struct {
	ctx        context.Context
	httpClient *http.Client
	baseURL    string // scheme://host[:port], no trailing slash
	username   string
	password   string
	basicAuth  bool

	// sessionToken/sessionPath are set by createSession for AuthMode=="session".
	// When sessionToken is non-empty it is used (X-Auth-Token) in preference to
	// Basic-Auth, matching gofish's auth precedence.
	sessionToken string
	sessionPath  string

	// sem bounds concurrent in-flight requests to Config.MaxConcurrent,
	// mirroring gofish's ClientConfig.MaxConcurrentRequests semaphore.
	sem chan struct{}
}

// newRFAPI builds an rfAPI bound to ctx for the lifetime of the connection,
// matching gofish's ConnectContext (which likewise captures ctx once at
// connect time and reuses it for every subsequent request).
func newRFAPI(ctx context.Context, httpClient *http.Client, baseURL, username, password string, basicAuth bool, maxConcurrent int) (*rfAPI, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("redfish: parse BaseURL: %w", err)
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	return &rfAPI{
		ctx:        ctx,
		httpClient: httpClient,
		baseURL:    strings.TrimSuffix(u.String(), "/"),
		username:   username,
		password:   password,
		basicAuth:  basicAuth,
		sem:        make(chan struct{}, maxConcurrent),
	}, nil
}

func (a *rfAPI) resolve(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return a.baseURL + path
}

// Get issues a GET. Signature matches gofish's (*gofish.APIClient).Get.
func (a *rfAPI) Get(path string) (*http.Response, error) {
	return a.do(http.MethodGet, path, nil)
}

// Post issues a POST with a JSON body. Signature matches gofish's
// (*gofish.APIClient).Post.
func (a *rfAPI) Post(path string, payload any) (*http.Response, error) {
	return a.do(http.MethodPost, path, payload)
}

// Patch issues a PATCH with a JSON body. Signature matches gofish's
// (*gofish.APIClient).Patch.
func (a *rfAPI) Patch(path string, payload any) (*http.Response, error) {
	return a.do(http.MethodPatch, path, payload)
}

// Delete issues a DELETE (used to log out of a session).
func (a *rfAPI) Delete(path string) (*http.Response, error) {
	return a.do(http.MethodDelete, path, nil)
}

// acquire/release bound concurrent requests to len(sem)'s capacity, ctx-aware
// like gofish's acquireSemaphore/releaseSemaphore.
func (a *rfAPI) acquire() error {
	select {
	case <-a.ctx.Done():
		return a.ctx.Err()
	case a.sem <- struct{}{}:
		return nil
	}
}

func (a *rfAPI) release() { <-a.sem }

func (a *rfAPI) do(method, path string, payload any) (*http.Response, error) {
	if path == "" {
		return nil, fmt.Errorf("redfish: unable to execute request, no target provided")
	}
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("redfish: encode request body: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(a.ctx, method, a.resolve(path), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if a.sessionToken != "" {
		req.Header.Set("X-Auth-Token", a.sessionToken)
	} else if a.basicAuth && a.username != "" {
		req.SetBasicAuth(a.username, a.password)
	}

	if err := a.acquire(); err != nil {
		return nil, err
	}
	resp, err := a.httpClient.Do(req)
	a.release()
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent:
		return resp, nil
	default:
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		return nil, &rfHTTPError{Method: method, URL: req.URL.String(), StatusCode: resp.StatusCode, Body: string(errBody)}
	}
}

// getJSON GETs path and decodes the JSON response body into out.
func (a *rfAPI) getJSON(path string, out any) error {
	resp, err := a.Get(path)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("redfish: decode %s: %w", path, err)
	}
	return nil
}

// createSession logs into the Redfish SessionService (POST {"UserName",
// "Password"} to the service root's Links.Sessions collection) and stores
// the returned X-Auth-Token/Location for reuse by later requests and eventual
// logout -- the thin equivalent of gofish's session-auth path (used when
// Config.AuthMode=="session" instead of "basic").
func (a *rfAPI) createSession(sessionsLink, username, password string) error {
	if sessionsLink == "" {
		return fmt.Errorf("redfish: service root did not advertise a Sessions link (Links.Sessions)")
	}
	resp, err := a.Post(sessionsLink, map[string]string{"UserName": username, "Password": password})
	if err != nil {
		return fmt.Errorf("redfish: create session: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	token := resp.Header.Get("X-Auth-Token")
	if token == "" {
		return fmt.Errorf("redfish: create session: no X-Auth-Token in response")
	}
	a.sessionToken = token
	if loc := resp.Header.Get("Location"); loc != "" {
		if u, err := url.ParseRequestURI(loc); err == nil {
			loc = u.RequestURI()
		}
		a.sessionPath = loc
	}
	return nil
}

// logout deletes the active session, if any (best-effort, mirrors gofish's
// APIClient.Logout -- a silent no-op for Basic-Auth, where no session exists).
func (a *rfAPI) logout() {
	if a == nil || a.sessionPath == "" {
		return
	}
	if resp, err := a.Delete(a.sessionPath); err == nil {
		_ = resp.Body.Close()
	}
}

// rfHTTPError is returned for a non-2xx Redfish response (mirrors gofish's
// common.ConstructError: status code plus a truncated body for diagnosis).
type rfHTTPError struct {
	Method     string
	URL        string
	StatusCode int
	Body       string
}

func (e *rfHTTPError) Error() string {
	msg := strings.TrimSpace(e.Body)
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	if msg == "" {
		return fmt.Sprintf("redfish: %s %s: HTTP %d", e.Method, e.URL, e.StatusCode)
	}
	return fmt.Sprintf("redfish: %s %s: HTTP %d: %s", e.Method, e.URL, e.StatusCode, msg)
}

// fetchOne GETs a single resource link and decodes it into T. An empty link
// (resource not present on this BMC) yields (nil, nil), matching gofish's
// pattern of returning a nil resource without error when the parent's link
// field was empty (e.g. Chassis.Thermal()/Power() when unsupported).
func fetchOne[T any](a *rfAPI, link string) (*T, error) {
	if link == "" {
		return nil, nil
	}
	var out T
	if err := a.getJSON(link, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// fetchCollectionLinks GETs a Redfish collection at link and returns the
// flattened list of member @odata.id values, following Members@odata.nextLink
// pagination (gofish's common.CollectList does the same) up to
// maxCollectionPages pages.
func fetchCollectionLinks(a *rfAPI, link string) ([]string, error) {
	var out []string
	next := link
	for pages := 0; next != ""; pages++ {
		if pages >= maxCollectionPages {
			return nil, fmt.Errorf("redfish: collection %q exceeded %d pages (nextLink loop?)", link, maxCollectionPages)
		}
		var col rfCollection
		if err := a.getJSON(next, &col); err != nil {
			return nil, err
		}
		for _, m := range col.Members {
			if m.ODataID != "" {
				out = append(out, m.ODataID)
			}
		}
		next = col.NextLink
	}
	return out, nil
}

// fetchCollection GETs a Redfish collection at link, then GETs and decodes
// each member individually into T (mirrors gofish's
// common.GetCollectionObjects: one GET for the collection, one GET per
// member). An empty link yields (nil, nil).
//
// Like gofish's GetCollectionObjects, a per-member fetch failure does not
// discard members that succeeded: it returns the partial slice alongside the
// first error encountered. Callers that only proceed on err==nil already
// discard partial results themselves (matching their pre-existing gofish-era
// behavior); callers that instead check len(result)==0 before treating err as
// fatal (e.g. ListSensors' chassis loop) rely on getting the partial slice.
func fetchCollection[T any](a *rfAPI, link string) ([]*T, error) {
	if link == "" {
		return nil, nil
	}
	links, err := fetchCollectionLinks(a, link)
	if err != nil {
		return nil, err
	}
	out := make([]*T, 0, len(links))
	var firstErr error
	for _, l := range links {
		var item T
		if err := a.getJSON(l, &item); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, &item)
	}
	return out, firstErr
}
