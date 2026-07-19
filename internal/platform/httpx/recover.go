package httpx

import "net/http"

func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				WriteError(w, r, nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
