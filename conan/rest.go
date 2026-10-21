package conan

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/easydev-org/easyblobs/core"
)

func (s *State) conans(w http.ResponseWriter, r *http.Request, path string) {
	rest := strings.TrimPrefix(path, "conans/")
	parts := strings.Split(rest, "/")
	// Top-level search: /conans/search?q=<pattern>. Return local repos merged
	// with upstream results so a pulled + pushed recipe is findable.
	if parts[0] == "search" {
		s.search(w, r)
		return
	}
	// /conans/{name}/{ver}/{user}/{channel}/...
	if len(parts) < 5 {
		s.proxyRecipe(w, r, path)
		return
	}
	name, ver := parts[0], parts[1]
	user, channel := parts[2], parts[3]
	// /conans/{name}/{ver}/{user}/{channel}/revisions[...]
	_ = user
	_ = channel
	// Pull-through for METADATA ONLY (revisions, latest, listing): a recipe with
	// nothing cached locally is answered straight from the upstream so the
	// client can discover its revisions.
	//
	// It must NOT swallow file downloads. This condition used to cover every
	// non-package GET, so `/revisions/<rev>/files/conanfile.py` was proxied and
	// nothing was ever stored - a client pulled a recipe, the mirror kept
	// nothing, and the next pull went upstream again. File GETs fall through to
	// fileGet, which now caches on read.
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		!strings.Contains(rest, "/packages/") &&
		!strings.Contains(rest, "/files/") &&
		!s.recipeKnown(r.Context(), name, ver) {
		s.proxyRecipe(w, r, path)
		return
	}
	if parts[4] == "revisions" {
		// DELETE /revisions/{rev} — remove the whole recipe.
		if r.Method == http.MethodDelete {
			if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "conan") {
				return
			}
			vs, _ := s.Registry.Meta.ListVersions(r.Context(), "conan", name)
			for _, v := range vs {
				removeVersion(s.Registry, name, v, r.Context())
			}
			removeVersion(s.Registry, name, ver, r.Context())
			w.WriteHeader(http.StatusOK)
			return
		}
		// Package-path: /revisions/{rev}/packages/{pid}/revisions[/{prev}/files...]
		if strings.Contains(rest, "/packages/") {
			if strings.HasSuffix(rest, "/latest") {
				// Package revision lookup: return the recorded package revision
				// if we have package files for this pid, else proxy.
				if prev, ok := s.packageRev(r.Context(), name, ver, parts); ok {
					easyblobkit.JSON(w, http.StatusOK, map[string]any{"revision": prev, "time": s.revTime(r, name, ver)})
					return
				}
				s.replyOrProxy(w, r, nil, "/v2/conans/"+rest)
				return
			}
			if strings.HasSuffix(rest, "/revisions") {
				// Package binary revisions list: empty (brand-new package).
				easyblobkit.JSON(w, http.StatusOK, map[string]any{"revisions": []any{}})
				return
			}
			if fi := strings.Index(rest, "/files"); fi >= 0 {
				filename := strings.TrimPrefix(rest[fi+len("/files"):], "/")
				filename = strings.SplitN(filename, "/", 2)[0]
				// Package /files LISTING (no filename): return the dict of
				// package files for the pid, so the client knows what to GET.
				if filename == "" {
					if files := s.packageFiles(r.Context(), name, parts); len(files) > 0 {
						easyblobkit.JSON(w, http.StatusOK, map[string]any{"files": files})
						return
					}
					// Nothing cached locally: proxy the upstream listing.
					s.replyOrProxy(w, r, nil, "/v2/conans/"+rest)
					return
				}
				// For package file PUTs, persist the pid + prev so a later
				// /packages/{pid}/latest and file download resolve locally.
				verSlot := filename
				if pi := strings.Index(rest, "/packages/"); pi >= 0 {
					seg := rest[pi+len("/packages/"):]
					pseg := strings.SplitN(seg, "/", 2)
					if len(pseg) == 2 {
						pid, ptail := pseg[0], pseg[1]
						prev := strings.SplitN(ptail, "/", 2)[0]
						if prev == "revisions" {
							// /packages/{pid}/revisions/{prev}/files/... -> the
							// package revision is the segment after "revisions".
							p2 := strings.SplitN(ptail, "/", 3)
							if len(p2) == 3 {
								prev = p2[1]
							}
						}
						verSlot = "package/" + pid + "/" + prev + "/" + filename
					}
				}
				if r.Method == http.MethodPut {
					if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "conan") {
						return
					}
					easyblobkit.LimitBody(w, r)
					data, err := io.ReadAll(r.Body)
					if err != nil {
						easyblobkit.WriteReadErr(w, err)
						return
					}
					storeFile(s.Registry, name, ver, verSlot, data, r.Context())
					easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
					return
				}
				if data, ok := s.loadFile(r.Context(), name, ver, verSlot); ok {
					easyblobkit.OctetResponse(w, r, data)
					return
				}
				if data, ok := s.loadFile(r.Context(), name, ver, filename); ok {
					easyblobkit.OctetResponse(w, r, data)
					return
				}
				// Package file not cached locally: pull it through from the
				// upstream (center2), caching the bytes under this slot.
				if data, ok := s.fetchUpstreamFile(r, path); ok {
					storeFile(s.Registry, name, ver, verSlot, data, r.Context())
					easyblobkit.OctetResponse(w, r, data)
					return
				}
				easyblobkit.Error(w, http.StatusNotFound, "not found")
				return
			}
			// other package sub-paths -> proxy.
			s.replyOrProxy(w, r, nil, "/v2/conans/"+rest)
			return
		}
		// /conans/{name}/{ver}/{user}/{channel}/revisions[/{rev}/files[/{filename}]]
		// Serve a recipe's file listing / file bytes from local storage when the
		// recipe was uploaded; for a rev not in the local cache, fall back to
		// serving whatever files we did record (recipe files persist under the
		// recipe name regardless of rev).
		filesIdx := func(rest string) int {
			return strings.Index(rest, "/files")
		}
		if fi := filesIdx(rest); fi >= 0 {
			countParts := len(strings.Split(rest, "/"))
			tail := rest[fi+len("/files"):]
			var filename string
			if after, ok := strings.CutPrefix(tail, "/"); ok {
				filename = strings.SplitN(after, "/", 2)[0]
			}
			_ = countParts
			if filename == "" {
				// Listing: return a dict filename->download URL (Conan's
				// /files response shape), for the recipe files we recorded.
				easyblobkit.JSON(w, http.StatusOK, map[string]any{
					"files": s.recipeFiles(r.Context(), name),
				})
				return
			}
			if r.Method == http.MethodPut {
				if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "conan") {
					return
				}
				easyblobkit.LimitBody(w, r)
				data, err := io.ReadAll(r.Body)
				if err != nil {
					easyblobkit.WriteReadErr(w, err)
					return
				}
				storeFile(s.Registry, name, ver, filename, data, r.Context())
				// The recipe revision is in the URL; remember it so a later
				// /latest and /revisions listing returns the same value.
				if len(parts) > 5 && parts[4] == "revisions" {
					storeRecipeRev(s.Registry, name, ver, parts[5], r.Context())
				}
				u := s.base() + "/v2/conans/" + easyblobkit.URLencode(name) + "/" + easyblobkit.URLencode(ver) + "/_/_/revisions/0/files/" + easyblobkit.URLencode(filename)
				easyblobkit.JSON(w, http.StatusOK, map[string]any{"files": map[string]any{filename: u}})
				return
			}
			if data, ok := s.loadFile(r.Context(), name, ver, filename); ok {
				easyblobkit.OctetResponse(w, r, data)
				return
			}
			// Recipe file not cached: pull it through from upstream.
			if data, ok := s.fetchUpstreamFile(r, path); ok {
				storeFile(s.Registry, name, ver, filename, data, r.Context())
				easyblobkit.OctetResponse(w, r, data)
				return
			}
			easyblobkit.Error(w, http.StatusNotFound, "not found")
			return
		}
		if len(parts) == 5 {
			if r.Method == http.MethodGet {
				if !s.recipeExists(r.Context(), name, ver) {
					easyblobkit.JSON(w, http.StatusOK, map[string]any{"revisions": []any{}})
					return
				}
				rev := s.loadRecipeRev(r.Context(), name, ver)
				easyblobkit.JSON(w, http.StatusOK, map[string]any{
					"reference": name + "/" + ver + "@_/_",
					"revisions": []any{map[string]any{"revision": rev, "time": s.revTime(r, name, ver)}},
				})
				return
			}
		}
		// /revisions/{rev}/search — list this recipe's package binaries. A
		// locally-published recipe has no upstream counterpart, so answer from
		// local storage (empty when no binary was uploaded): proxying would
		// 404 and abort the client.
		if strings.HasSuffix(rest, "/search") && s.recipeExists(r.Context(), name, ver) {
			easyblobkit.JSON(w, http.StatusOK, s.packageSearch(r.Context(), name))
			return
		}
		// remaining sub-paths (files listing, download_urls, upload_urls,
		// packages) — proxy upstream when not locally known.
		s.replyOrProxy(w, r, nil, "/v2/conans/"+rest)
		return
	}

	sub := ""
	if len(parts) > 5 {
		sub = strings.Join(parts[5:], "/")
	}

	switch {
	case len(parts) == 5 || sub == "latest":
		if r.Method == http.MethodDelete {
			if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "conan") {
				return
			}
			// Remove the recipe + all its recorded files/package slots.
			vs, _ := s.Registry.Meta.ListVersions(r.Context(), "conan", name)
			for _, v := range vs {
				removeVersion(s.Registry, name, v, r.Context())
			}
			removeVersion(s.Registry, name, ver, r.Context())
			easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		if _, err := s.Registry.Meta.Get(r.Context(), "conan", name, ver); err != nil {
			rev := s.loadRecipeRev(r.Context(), name, ver)
			fallback := map[string]any{"revision": rev, "time": s.revTime(r, name, ver)}
			s.replyOrProxy(w, r, fallback, "/v2/conans/"+easyblobkit.URLencode(name)+"/"+easyblobkit.URLencode(ver)+"/_/_/latest")
			return
		}
		rev := s.loadRecipeRev(r.Context(), name, ver)
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"revision": rev, "time": s.revTime(r, name, ver)})
	case strings.HasPrefix(sub, "revisions"):
		if _, err := s.Registry.Meta.Get(r.Context(), "conan", name, ver); err != nil {
			// Recipe not yet uploaded: return 404 so the Conan client treats
			// it as brand new (it does NOT re-list revisions on 404 and
			// proceeds to upload).
			easyblobkit.Error(w, http.StatusNotFound, "revision not found")
			return
		}
		rev := s.loadRecipeRev(r.Context(), name, ver)
		easyblobkit.JSON(w, http.StatusOK, map[string]any{
			"revisions": []any{map[string]any{"revision": rev, "time": s.revTime(r, name, ver)}},
		})
	case strings.HasPrefix(sub, "revisions/") && strings.HasSuffix(sub, "/files"):
		s.files(w, r, "revisions/"+strings.TrimSuffix(sub, "/files"))
	case strings.Contains(sub, "revisions/") && strings.Contains(sub, "/files/"):
		s.fileGet(w, r, name, ver, sub)
	case strings.HasSuffix(sub, "/upload_urls"):
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"upload_urls": map[string]any{}})
	case strings.HasSuffix(sub, "/download_urls"):
		s.downloadURLs(w, r, name, ver, sub)
	case strings.HasSuffix(sub, "/search"):
		s.replyOrProxy(w, r, map[string]any{"results": nil}, "/v2/conans/"+rest)
	default:
		s.proxyRecipe(w, r, path)
	}
}

func (s *State) fileGet(w http.ResponseWriter, r *http.Request, name, ver, sub string) {
	// /revisions/{rev}/files/{filename}
	idx := strings.Index(sub, "/files/")
	if idx < 0 {
		s.proxyRecipe(w, r, "/conans/"+name+"/"+ver+"/"+sub)
		return
	}
	filename := sub[idx+len("/files/"):]
	if r.Method == http.MethodPut {
		if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "conan") {
			return
		}
		easyblobkit.LimitBody(w, r)
		data, err := io.ReadAll(r.Body)
		if err != nil {
			easyblobkit.WriteReadErr(w, err)
			return
		}
		storeFile(s.Registry, name, ver, filename, data, r.Context())
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if data, ok := s.loadFile(r.Context(), name, ver, filename); ok {
		easyblobkit.OctetResponse(w, r, data)
		return
	}
	// Cache on READ.
	//
	// Until now only PUT (upload) called storeFile, so `conan download` received
	// the bytes while this mirror kept nothing: every later request went back
	// upstream. That is a proxy, not a mirror - and in an air-gapped or
	// egress-restricted deployment the first read is all you get.
	//
	// Fetch the file, persist it exactly as an upload would (storeFile indexes
	// it so loadFile finds it next time), then serve it. A genuine upstream 404
	// still propagates so the client can fall back to building from source.
	if data, ok := s.fetchUpstreamFile(r, "/conans/"+name+"/"+ver+"/"+sub); ok {
		storeFile(s.Registry, name, ver, filename, data, r.Context())
		easyblobkit.OctetResponse(w, r, data)
		return
	}
	s.replyOrProxy(w, r, nil, "/v2/conans/"+easyblobkit.URLencode(name)+"/"+easyblobkit.URLencode(ver)+"/"+sub)
}

func (s *State) downloadURLs(w http.ResponseWriter, r *http.Request, name, ver, sub string) {
	urls := map[string]any{}
	// List known files for the recipe and assign a self URL.
	if _, err := s.Registry.Meta.Get(r.Context(), "conan", name, ver); err != nil {
		s.proxyRecipe(w, r, "/conans/"+name+"/"+ver+"/"+sub)
		return
	}
	for _, fn := range []string{"conanfile.py", "conanmanifest.txt", "conaninfo.txt"} {
		if _, ok := s.loadFile(r.Context(), name, ver, fn); ok {
			urls[fn] = s.base() + "/v2/files/" + easyblobkit.URLencode(name) + "/" + easyblobkit.URLencode(ver) + "/_/_/0/" + fn
		}
	}
	easyblobkit.JSON(w, http.StatusOK, map[string]any{"download_urls": urls})
}

// revTime returns the RFC3339 time to advertise for a Conan revision: the
// version's publish (or cache) time, falling back to now so the client never
// sees a bogus fixed date.
func (s *State) revTime(r *http.Request, name, ver string) string {
	if t, ok := s.Registry.ArtifactTime(r.Context(), "conan", name, ver); ok {
		return t
	}
	return easyblobkit.PublishTime(easyblobkit.Artifact{}, time.Time{})
}

func (s *State) files(w http.ResponseWriter, r *http.Request, rest string) {
	// /files/{name}/{ver}/{user}/{channel}/{rev}/recipe/{filename}
	if r.Method == http.MethodPut {
		if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "conan") {
			return
		}
	}

	// For GET of "/{version}/files/{name}/{ver}/{user}/{channel}/{rev}/recipe/{file}"
	// we route based on the raw request path (handled in ServeHTTP via conans).
	// This branch stubs the listing endpoint.
	easyblobkit.JSON(w, http.StatusOK, map[string]any{"files": []any{}})
}
