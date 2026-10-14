package conan

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

// search returns a merged local + upstream Conan search.
func (s *State) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	pattern := q
	if pattern == "" || pattern == "*" {
		pattern = "*"
	}
	// A Conan pattern can be "name/version@user/channel"; only the name part
	// participates in the recipe search.
	if i := strings.IndexAny(pattern, "/@"); i >= 0 {
		pattern = pattern[:i]
	}
	var local []string
	repos, _ := s.Registry.Meta.ListRepositoriesByFormat(r.Context(), "conan")
	for _, repo := range repos {
		if q != "" && !globMatch(repo, pattern) {
			continue
		}
		versions, _ := s.Registry.Meta.ListVersions(r.Context(), "conan", repo)
		recipeVersions := map[string]bool{}
		for _, v := range versions {
			// Recipe versions are the plain semver entries; files and package
			// slots use slash-separated keys.
			if v != "" && !strings.ContainsAny(v, "/") {
				recipeVersions[v] = true
			}
		}
		if len(recipeVersions) == 0 {
			continue
		}
		for v := range recipeVersions {
			local = append(local, repo+"/"+v+"@_/_")
		}
	}
	// Merge upstream results (pull-through) so public packages still appear,
	// filtered by the same name pattern.
	upstream := map[string]bool{}
	if remote, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "conan"}); err == nil {
		if body, err := remote.GetBytes(r.Context(), "/v2/conans/search?q="+easyblobkit.URLencode(q)); err == nil {
			var m map[string]any
			if json.Unmarshal(body, &m) == nil {
				if res, ok := m["results"].([]any); ok {
					for _, x := range res {
						str, ok := x.(string)
						if !ok {
							continue
						}
						name := str
						if i := strings.IndexAny(name, "/@"); i >= 0 {
							name = name[:i]
						}
						if q == "" || globMatch(name, pattern) {
							upstream[str] = true
						}
					}
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, l := range local {
		seen[l] = true
	}
	for u := range upstream {
		if !seen[u] {
			local = append(local, u)
		}
	}
	if local == nil {
		local = []string{}
	}
	easyblobkit.JSON(w, http.StatusOK, map[string]any{"results": local})
}

func globMatch(name, pattern string) bool {
	return regexGlob(pattern, name)
}

func regexGlob(pattern, s string) bool {
	// QuoteMeta turns the glob's "*" into "\*"; convert those back to ".*"
	// (quoting everything else first keeps regex metacharacters literal).
	quoted := strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*")
	re, err := regexp.Compile("^" + quoted + "$")
	if err != nil {
		return false
	}
	return re.MatchString(s)
}
