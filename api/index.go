// TEMPORARY diagnostic handler — dumps what Vercel passes the function so we can
// fix path routing. Will be reverted to the real handler.
package handler

import (
	"fmt"
	"net/http"
	"sort"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "method=%s\n", r.Method)
	fmt.Fprintf(w, "url.path=%q\n", r.URL.Path)
	fmt.Fprintf(w, "url.rawpath=%q\n", r.URL.RawPath)
	fmt.Fprintf(w, "url.rawquery=%q\n", r.URL.RawQuery)
	fmt.Fprintf(w, "requesturi=%q\n", r.RequestURI)
	fmt.Fprintf(w, "host=%q\n", r.Host)

	keys := make([]string, 0, len(r.Header))
	for k := range r.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintln(w, "--- headers ---")
	for _, k := range keys {
		fmt.Fprintf(w, "%s: %s\n", k, r.Header.Get(k))
	}
}
