package powerstatus

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func ReadToken(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0007 != 0 || info.Mode().Perm()&0020 != 0 {
		return "", errors.New("token file must be regular and private")
	}
	b, err := readBounded(path, 4096)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if len(token) < 32 || strings.ContainsAny(token, " \t\n\r") {
		return "", errors.New("status token must have at least 32 non-whitespace characters")
	}
	return token, nil
}

func Handler(path string, nodeID int64, clock Clock, token string) http.Handler {
	expected := sha256.Sum256([]byte("Bearer " + token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		presented := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(presented[:], expected[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="node-status"`)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("{\"error\":\"unauthorized\"}\n"))
			return
		}
		if r.URL.Path != "/v1/power-state" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte("{\"error\":\"method_not_allowed\"}\n"))
			return
		}
		snapshot, err := ReadSnapshot(path, nodeID, clock)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("{\"error\":\"snapshot_unavailable\"}\n"))
			return
		}
		_ = json.NewEncoder(w).Encode(snapshot)
	})
}

func Listen(unixSocket, tcpAddress string) (net.Listener, error) {
	if (unixSocket == "") == (tcpAddress == "") {
		return nil, errors.New("choose exactly one Unix socket or loopback TCP address")
	}
	if tcpAddress != "" {
		host, _, err := net.SplitHostPort(tcpAddress)
		if err != nil {
			return nil, err
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, errors.New("TCP listener must use a loopback IP")
		}
		return net.Listen("tcp", tcpAddress)
	}
	if !filepath.IsAbs(unixSocket) {
		return nil, errors.New("Unix socket path must be absolute")
	}
	listener, err := net.Listen("unix", unixSocket)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(unixSocket, 0660); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}
