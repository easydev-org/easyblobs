package easyblobkit

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// identityClient is a tiny EasyVCS `/api/v1/whoami` client (inlined to drop the
// private github.com/easy-platform/platform dependency). It resolves a bearer
// token to the authenticated username, with a short TTL cache.
type identityClient struct {
	baseURL string
	http    *http.Client
	mu      sync.Mutex
	cache   map[string]identityEntry
}

type identityEntry struct {
	username string
	exp      time.Time
}

// identityDefaultTTL mirrors the former identity.DefaultTTL.
const identityDefaultTTL = 30 * time.Second

type identityIdentity struct {
	Authenticated bool   `json:"authenticated"`
	Username      string `json:"username"`
}

func newIdentityClient(baseURL string) *identityClient {
	return &identityClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 5 * time.Second},
		cache:   map[string]identityEntry{},
	}
}

func (c *identityClient) Whoami(ctx context.Context, token string) (identityIdentity, error) {
	c.mu.Lock()
	if e, ok := c.cache[token]; ok && time.Now().Before(e.exp) {
		c.mu.Unlock()
		return identityIdentity{Authenticated: e.username != "", Username: e.username}, nil
	}
	c.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/whoami", nil)
	if err != nil {
		return identityIdentity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return identityIdentity{}, err
	}
	defer resp.Body.Close()
	var out identityIdentity
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return identityIdentity{}, err
	}
	c.mu.Lock()
	c.cache[token] = identityEntry{username: out.Username, exp: time.Now().Add(identityDefaultTTL)}
	c.mu.Unlock()
	return out, nil
}

// RemoteAuth is the delegating Auth: instead of a local credential table it
// validates every bearer/raw token against **easyvcs**, the platform's single
// identity authority (via platform/identity's GET /api/v1/whoami, with a short
// TTL cache). This is what lets agent-gateway pass a user's own token straight
// through to artifact: there is one permission model, enforced in one place.
//
// It is selected at startup with EASYBLOBS_AUTH=easyvcs (see cmd/auth.go); the
// static TokenAuth remains the default for local/testing use.
//
// Namespace ownership: platform/identity answers *who* a token is, not *what
// namespaces it may write*. So an optional NamespaceAuthorizer decides write
// access per namespace; when none is configured an authenticated user may write
// any namespace (a deliberately permissive interim policy — see DEVELOP.md).
type RemoteAuth struct {
	id *identityClient
	ns NamespaceAuthorizer

	mintedMu sync.Mutex
	minted   map[string]*mintedToken
}

// NamespaceAuthorizer decides whether an authenticated user may write to a
// namespace. It is the seam where an easyvcs namespace-ownership check will
// plug in once platform exposes it; nil means "authenticated ⇒ allowed".
type NamespaceAuthorizer interface {
	CanWriteNS(ctx context.Context, username, namespace string) bool
}

// NewRemoteAuth builds a RemoteAuth against the easyvcs base URL
// (e.g. "http://easyvcs-server.worker.svc.cluster.local"). It uses
// identity.DefaultTTL for the whoami cache.
func NewRemoteAuth(easyvcsURL string) *RemoteAuth {
	return NewRemoteAuthWithAuthorizer(easyvcsURL, nil)
}

// NewRemoteAuthWithAuthorizer is NewRemoteAuth with an explicit namespace
// authorizer (nil = authenticated users may write any namespace).
func NewRemoteAuthWithAuthorizer(easyvcsURL string, ns NamespaceAuthorizer) *RemoteAuth {
	return &RemoteAuth{
		id:     newIdentityClient(easyvcsURL),
		ns:     ns,
		minted: map[string]*mintedToken{},
	}
}

// user resolves a token to an easyvcs username, or "" when the token is not a
// valid, enabled identity. It is the single point where every credential form
// funnels into easyvcs.
func (a *RemoteAuth) user(ctx context.Context, token string) string {
	if token == "" {
		return ""
	}
	id, err := a.id.Whoami(ctx, token)
	if err != nil || !id.Authenticated {
		return ""
	}
	return id.Username
}

// rawToken extracts the caller's credential from a request in every scheme the
// platform uses: Bearer/token prefixes, a bare token (cargo), Basic (the
// password carries the token), and the X-NuGet-ApiKey header. It mirrors
// TokenAuth.Authenticate's header handling but returns the raw token so it can
// be validated remotely.
func rawToken(r *http.Request) string {
	raw := r.Header.Get("Authorization")
	if prefix, rest, ok := cutPrefix(raw, "Bearer "); ok && prefix != "" {
		return rest
	}
	if rest, ok := stripTokenPrefix(raw); ok {
		return rest
	}
	if raw != "" && !hasAnyPrefix(raw, "Bearer ", "token ", "Token ", "Basic ") {
		return raw
	}
	if basic, ok := strings.CutPrefix(raw, "Basic "); ok {
		if decoded, err := base64.StdEncoding.DecodeString(basic); err == nil {
			if _, secret, ok := strings.Cut(string(decoded), ":"); ok {
				return strings.TrimSpace(secret)
			}
		}
	}
	if key := r.Header.Get("X-NuGet-ApiKey"); key != "" {
		return key
	}
	return ""
}

// Authenticate implements Auth: the easyvcs username for a valid credential,
// else "".
func (a *RemoteAuth) Authenticate(ctx context.Context, r *http.Request) string {
	return a.user(ctx, rawToken(r))
}

// CheckBearer implements Auth. A bearer minted by IssueToken is honored locally
// (its grant bounds the action); any other bearer is validated against easyvcs.
func (a *RemoteAuth) CheckBearer(ctx context.Context, token, wantedScope string) (string, bool) {
	_, action, ok := splitAction(wantedScope)
	if !ok {
		return "", false
	}
	if m, ok := a.lookupMinted(token); ok {
		if !actionAllowedByScopes(action, m.scopes, m.level) {
			return "", false
		}
		return m.username, true
	}
	u := a.user(ctx, token)
	if u == "" {
		return "", false
	}
	if action == "pull" || action == "" {
		return u, true
	}
	// A write action: the caller must be allowed to write the target namespace.
	if !a.mayWriteNS(ctx, u, namespaceOf(wantedScope)) {
		return "", false
	}
	return u, true
}

// CheckToken implements Auth: a raw token (cargo / npm publish) is valid when
// easyvcs authenticates it.
func (a *RemoteAuth) CheckToken(ctx context.Context, token string) (string, bool) {
	u := a.user(ctx, token)
	return u, u != ""
}

// CheckBasic implements Auth: the password carries the token.
func (a *RemoteAuth) CheckBasic(ctx context.Context, _, pass string) bool {
	return a.user(ctx, pass) != ""
}

// IssueToken implements Auth: mint a random OCI bearer bound to username +
// scopes + ttl, remembered in-process so CheckBearer recognizes it. This backs
// the /v2/ token realm. A pull-only grant can never authorize a push.
func (a *RemoteAuth) IssueToken(ctx context.Context, username string, scopes []string, ttl time.Duration) string {
	if ttl <= 0 {
		ttl = time.Hour
	}
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is unrecoverable; fail closed (no token).
		return ""
	}
	tok := "ak_m_" + base64.RawURLEncoding.EncodeToString(b[:])
	a.mintedMu.Lock()
	now := time.Now()
	for k, m := range a.minted {
		if now.After(m.expires) {
			delete(a.minted, k)
		}
	}
	a.minted[tok] = &mintedToken{username: username, scopes: scopes, level: LevelWrite, expires: now.Add(ttl)}
	a.mintedMu.Unlock()
	return tok
}

// lookupMinted resolves a bearer issued by IssueToken, pruning it when expired.
func (a *RemoteAuth) lookupMinted(token string) (*mintedToken, bool) {
	a.mintedMu.Lock()
	defer a.mintedMu.Unlock()
	m, ok := a.minted[token]
	if !ok {
		return nil, false
	}
	if time.Now().After(m.expires) {
		delete(a.minted, token)
		return nil, false
	}
	return m, true
}

// mayWriteNS reports whether username may write namespace. With no authorizer
// configured, any authenticated user may (the interim policy).
func (a *RemoteAuth) mayWriteNS(ctx context.Context, username, namespace string) bool {
	if a.ns == nil {
		return true
	}
	return a.ns.CanWriteNS(ctx, username, namespace)
}

// namespaceOf extracts the namespace from a scope like
// "repository:<ns>/<name>:<action>": the first path segment of the repository.
// It returns "" when the scope has no namespace (the default namespace).
func namespaceOf(scope string) string {
	repo, _, ok := splitAction(scope)
	if !ok {
		return ""
	}
	if i := strings.Index(repo, ":"); i >= 0 {
		repo = repo[i+1:]
	}
	repo = strings.TrimPrefix(repo, "/")
	if i := strings.IndexByte(repo, '/'); i >= 0 {
		return repo[:i]
	}
	return repo
}
