package hub

// https.go: how the dashboard port is exposed (#197). With tls_cert and
// tls_key the port serves HTTPS only, from files that are read again when they
// change; without them it serves plain HTTP, and the hub warns at start when
// that port is reachable from other machines.

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"
)

// certRecheck is how often, at most, the certificate files are looked at
// again, so that a renewed certificate is used without a restart.
const certRecheck = time.Minute

// plainHTTPWarning is logged at start when the dashboard is served without
// TLS on an address other machines can reach.
const plainHTTPWarning = "the dashboard is served over plain HTTP on an address other machines can reach: " +
	"passwords, session cookies and enrollment tokens cross the network in the clear. " +
	"Set tls_cert and tls_key, or listen on 127.0.0.1 behind a reverse proxy with TLS (docs/installation.md)"

// certFiles serves the certificate in the files and reloads them when their
// modification time or size changes. A reload that fails keeps the previous
// certificate and is logged; only the first load must succeed.
type certFiles struct {
	certPath, keyPath string
	log               *slog.Logger
	now               func() time.Time

	mu      sync.Mutex
	current *tls.Certificate
	stamp   string
	checked time.Time
}

// newCertFiles loads the pair once, so that a wrong path or a key that does
// not match stops the hub at start with a clear message.
func newCertFiles(certPath, keyPath string, log *slog.Logger) (*certFiles, error) {
	if certPath == "" || keyPath == "" {
		return nil, errors.New("tls_cert and tls_key must be set together")
	}
	c := &certFiles{certPath: certPath, keyPath: keyPath, log: log, now: time.Now}
	stamp, err := c.stampOf()
	if err != nil {
		return nil, err
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("tls_cert and tls_key: %w", err)
	}
	c.current, c.stamp, c.checked = &cert, stamp, c.now()
	return c, nil
}

// stampOf identifies the current content of both files cheaply.
func (c *certFiles) stampOf() (string, error) {
	out := ""
	for _, p := range []string{c.certPath, c.keyPath} {
		fi, err := os.Stat(p)
		if err != nil {
			return "", fmt.Errorf("tls file: %w", err)
		}
		out += fmt.Sprintf("%s:%d:%d;", p, fi.ModTime().UnixNano(), fi.Size())
	}
	return out, nil
}

// get is the tls.Config GetCertificate callback.
func (c *certFiles) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if now.Sub(c.checked) < certRecheck {
		return c.current, nil
	}
	c.checked = now
	stamp, err := c.stampOf()
	if err != nil {
		c.log.Warn("cannot read the dashboard certificate again; the one already loaded is still used", "err", err)
		return c.current, nil
	}
	if stamp == c.stamp {
		return c.current, nil
	}
	cert, err := tls.LoadX509KeyPair(c.certPath, c.keyPath)
	if err != nil {
		// Often the moment between writing the certificate and the key: the
		// next look, a minute later, picks up the pair.
		c.log.Warn("the dashboard certificate files changed but cannot be loaded; the one already loaded is still used", "err", err)
		return c.current, nil
	}
	c.current, c.stamp = &cert, stamp
	c.log.Info("the dashboard certificate was loaded again", "file", c.certPath)
	return c.current, nil
}

// httpsConfig is the TLS configuration of the dashboard port. HTTP/2 is not
// offered: the live view is a WebSocket, which browsers open over HTTP/1.1.
func httpsConfig(files *certFiles) *tls.Config {
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: files.get,
		NextProtos:     []string{"http/1.1"},
	}
}

// exposeHTTP wraps ln in TLS when tls_cert and tls_key are set, and logs a
// warning when the port serves plain HTTP to other machines.
func (h *Hub) exposeHTTP(ln net.Listener) (net.Listener, error) {
	if h.cfg.TLS() {
		files, err := newCertFiles(h.cfg.TLSCert, h.cfg.TLSKey, h.log)
		if err != nil {
			return nil, err
		}
		h.log.Info("the dashboard is served over HTTPS", "cert", h.cfg.TLSCert)
		return tls.NewListener(ln, httpsConfig(files)), nil
	}
	if !loopbackAddr(ln.Addr()) {
		h.log.Warn(plainHTTPWarning, "addr", ln.Addr().String())
	}
	return ln, nil
}

// loopbackAddr reports whether a bound address can be reached from this
// machine only. A wildcard (0.0.0.0, ::) cannot.
func loopbackAddr(addr net.Addr) bool {
	if tcp, ok := addr.(*net.TCPAddr); ok {
		return tcp.IP.IsLoopback()
	}
	ap, err := netip.ParseAddrPort(addr.String())
	return err == nil && ap.Addr().Unmap().IsLoopback()
}
