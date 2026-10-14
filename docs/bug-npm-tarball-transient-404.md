# Bug report: npm tarball pull-through returns a spurious 404 under load

**Component:** `artifact` (easy-vcs) — npm adapter + pull-through fetch layer
**Severity:** high — breaks `npm ci` / `repo-build-image` intermittently
**Reporter:** tal-ai/documents (observed while migrating services to the
artifact registry)
**Status:** root cause identified in code; fix suggested below

## Summary

When many distinct npm tarballs are fetched through artifact **at once** (a
`npm ci` of a large lockfile, or a container build), some requests return
`404 Not Found` for tarballs that **do exist** upstream. The same URL returns
`200` seconds later, and the artifact metadata endpoint always lists the
version. This makes `npm ci` (and therefore `repo-build-image`) fail
non-deterministically.

## Reproduction / evidence

- First `repo-build-image` for `knowledge-base-doc-search` and
  `knowledge-base-pipeline` failed with:
  ```
  404 Not Found - GET http://artifact.worker.svc.cluster.local/artifacts/npm/secure-json-parse/-/secure-json-parse-4.1.0.tgz
  npm error 404 'secure-json-parse@...tgz' is not in this registry.
  ```
- A retry of the identical build succeeded; the tarball then served `200` with
  a sha512 matching the lockfile `integrity`.
- `GET /artifacts/npm/secure-json-parse` (packument) returned `200` listing
  `4.1.0` **while** the tarball 404'd — so the packument was reachable but the
  tarball was not.
- `fast-redact@3.5.0` showed the same pattern (both are transitive deps of
  `pino-pretty`, pulled by the `en` framework → many concurrent tarball
  fetches).
- Direct `https://registry.npmjs.org/...` returned `200` throughout, and via
  the egress proxy (`mihomo.develop.svc.cluster.local:7890`) 40/40 requests
  were `200`.

## Root cause

In `core/retry.go`, `retryableStatus` treats `0, 502, 503, 504, 429` as
transient and **`404` as definitive**:

```go
func retryableStatus(code int) bool {
	switch code {
	case 0, http.StatusBadGateway, http.StatusServiceUnavailable,
		http.StatusGatewayTimeout, http.StatusTooManyRequests:
		return true
	}
	return false
}
```

`Remote.getStable` therefore returns a `404` on the **first** attempt. In the
npm adapter, `npm/tarball.go` maps any upstream error on the tarball path to a
client `404`:

```go
data, err := remote.GetBytes(r.Context(), "/"+encodeName(name)+"/-/"+file)
if err != nil {
    data, err = s.tarballFromPackument(r.Context(), remote, name, file)
    if err != nil {
        easyblobkit.Error(w, http.StatusNotFound, "tarball not found")
        return
    }
}
```

So a **single** transient upstream failure on the tarball fetch surfaces as a
hard `404` to npm, which aborts the install. (The packument, fetched through
the TTL cache, usually succeeded because it was warm — hence the asymmetry
between a working packument and a 404 tarball.)

The project already has the intended abstraction for this —
`core.TransientUpstream(err)` — but **no adapter calls it**; every miss is
collapsed to `404`.

## Suggested fix

1. **Retry the npm tarball fetch a few times before giving up** (mirror the
   existing `go` module `.zip` retry from #44, or reuse `getStable`'s backoff),
   and
2. **map a still-failing fetch to `503` + `Retry-After` (not `404`) when
   `TransientUpstream(err)` is true**, reserving `404` for a definitive upstream
   `404`:

```go
data, err := remote.GetBytes(r.Context(), "/"+encodeName(name)+"/-/"+file)
if err != nil {
    data, err = s.tarballFromPackument(r.Context(), remote, name, file)
    if err != nil {
        if easyblobkit.TransientUpstream(err) {
            w.Header().Set("Retry-After", "2")
            easyblobkit.Error(w, http.StatusServiceUnavailable, "upstream temporarily unavailable")
            return
        }
        easyblobkit.Error(w, http.StatusNotFound, "tarball not found")
        return
    }
}
```

`503` makes npm retry (npm honors `Retry-After` and retries `5xx`), turning a
transient blip into a delayed success instead of a failed install. A defensive
option is to treat a tarball `404` as retryable for a bounded number of
attempts, since npm tarballs are immutable (a genuinely-missing tarball is
rare and retrying it a couple of times is cheap).

## Workaround (until fixed)

- Retry `npm ci` / the image build (the second attempt usually succeeds).
- Or pre-warm the lockfile's tarballs into the registry before building.
- Or keep the default npm registry pointed at a reachable mirror and only route
  internal scopes (`@tal-ai`) to artifact — but that reintroduces a public
  dependency and still hits the artifact path for `@tal-ai/en`'s transitive
  deps if they are fetched through artifact.

## Impact

Any consumer that installs from artifact's npm mount with a cold or
partially-cold cache is affected, most visibly inside container builds where
there is no retry. This directly contradicts the platform goal of consuming
internal packages from artifact.
