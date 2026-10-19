package pypi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/easydev-org/easyblobs/core"
)

// mergeSimpleProject fetches the upstream index (JSON or HTML per the client)
// and merges any locally-pushed versions on top. Returns true when the merged
// document was served. When the upstream is unreachable, returns false so the
// caller falls back to local-only.
func (s *State) mergeSimpleProject(w http.ResponseWriter, r *http.Request, name string, versions []string) bool {
	remote, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "pypi"})
	if err != nil {
		return false
	}
	path := "/simple/" + easyblobkit.URLencode(name) + "/"
	if wantsJSON(r) {
		jsonRemote := remote.WithHeader("Accept", "application/vnd.pypi.simple.v1+json")
		body, err := jsonRemote.GetBytes(r.Context(), path)
		if err != nil {
			return false
		}
		rewritten := mergeJSONHrefs(string(body), s.base(), name, versions, r, s.Registry)
		easyblobkit.Text(w, http.StatusOK, rewritten, "application/vnd.pypi.simple.v1+json")
		return true
	}
	body, err := remote.GetCached(r.Context(), sharedCache(), path)
	if err != nil {
		return false
	}
	rewritten := rewriteLinks(body, s.base(), name)
	easyblobkit.Text(w, http.StatusOK, rewritten, "text/html")
	return true
}

// mergeJSONHrefs rewrites upstream PEP 691 "url" fields to self and appends
// locally pushed files (analogous to the HTML rewriteLinks path). The caller
// supplies the request only to read local blobs' hashes. It is a free function
// (no State) so rewriteJSONHrefs can delegate without a receiver.
func mergeJSONHrefs(body, selfBase, project string, versions []string, req *http.Request, reg *easyblobkit.Registry) string {
	var doc map[string]any
	if json.Unmarshal([]byte(body), &doc) != nil {
		return body
	}
	files, _ := doc["files"].([]any)
	seen := map[string]bool{}
	for _, f := range files {
		if m, ok := f.(map[string]any); ok {
			if fname, _ := m["filename"].(string); fname != "" {
				seen[fname] = true
				m["url"] = selfBase + "/simple/" + easyblobkit.URLencode(project) + "/" + easyblobkit.URLencode(fname)
			}
		}
	}
	if files == nil {
		files = []any{}
	}
	// Merge local versions (a local re-publish must be authoritative).
	if len(versions) > 0 && req != nil && reg != nil {
		for _, v := range versions {
			art, err := reg.Meta.Get(req.Context(), "pypi", project, v)
			if err != nil {
				continue
			}
			for _, b := range art.Blobs {
				if b.Name == "" || seen[b.Name] {
					continue
				}
				entry := map[string]any{
					"filename":    b.Name,
					"url":         selfBase + "/simple/" + easyblobkit.URLencode(project) + "/" + easyblobkit.URLencode(b.Name),
					"hashes":      map[string]any{"sha256": b.Hex()},
					"size":        b.Size,
					"upload-time": easyblobkit.PublishTime(art, time.Time{}),
				}
				files = append(files, entry)
				seen[b.Name] = true
			}
		}
	}
	doc["files"] = files
	out, _ := json.Marshal(doc)
	return string(out)
}

func rewriteLinks(html, selfBase, project string) string {
	var sb strings.Builder
	rest := html
	for {
		start := strings.Index(rest, "<a ")
		if start < 0 {
			sb.WriteString(rest)
			break
		}
		sb.WriteString(rest[:start])
		after := rest[start:]
		gt := strings.Index(after, ">")
		close := strings.Index(after, "</a>")
		if gt < 0 {
			sb.WriteString(after)
			break
		}
		if close < 0 {
			sb.WriteString(after)
			break
		}
		fname := strings.TrimSpace(after[gt+1 : close])
		if i := strings.Index(fname, "#"); i >= 0 {
			fname = fname[:i]
		}
		href := selfBase + "/simple/" + easyblobkit.URLencode(project) + "/" + easyblobkit.URLencode(fname)
		sb.WriteString(`<a href="` + href + `">` + fname + "</a>")
		rest = after[close+len("</a>"):]
	}
	return sb.String()
}
