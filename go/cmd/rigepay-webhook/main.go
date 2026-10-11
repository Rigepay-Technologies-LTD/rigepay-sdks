// rigepay-webhook helps build and debug a webhook receiver.
//
//	rigepay-webhook listen -secret whsec_... -url https://yourapp.example.com/hooks/rigepay [-addr :8787]
//	rigepay-webhook verify -secret whsec_... -url https://... -timestamp 1769424764 -request-id ... -signature ... -body-file body.json
//
// listen runs a local receiver that checks every delivery's signature and prints it. Expose it with a
// tunnel, register the tunnel's public URL as your endpoint, and pass that same URL as -url.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	rigepay "github.com/Rigepay-Technologies-LTD/rigepay-go"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "listen":
		listen(os.Args[2:])
	case "verify":
		verify(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: rigepay-webhook listen|verify -secret ... -url ... (see -h)")
	os.Exit(2)
}

func listen(args []string) {
	fs := flag.NewFlagSet("listen", flag.ExitOnError)
	secret := fs.String("secret", "", "endpoint secret, including the whsec_ prefix")
	url := fs.String("url", "", "the endpoint URL exactly as registered with RigePay")
	addr := fs.String("addr", ":8787", "address to listen on")
	tolerance := fs.Duration("tolerance", 5*time.Minute, "how old a delivery may be")
	_ = fs.Parse(args)
	if *secret == "" || *url == "" {
		fs.Usage()
		os.Exit(2)
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		ok := rigepay.VerifyWebhook(*secret, *url, r.Header, body, *tolerance, time.Now())
		var pretty bytes.Buffer
		if json.Indent(&pretty, body, "", "  ") != nil {
			pretty.Reset()
			pretty.Write(body)
		}
		fmt.Printf("%s  %s  event=%s id=%s request=%s signature=%s\n%s\n\n", time.Now().Format(time.RFC3339), r.Method,
			r.Header.Get("X-RigePay-Event-Type"), r.Header.Get("X-RigePay-Event-ID"), r.Header.Get("X-RigePay-Request-ID"), map[bool]string{true: "valid", false: "INVALID"}[ok], pretty.String())
		if !ok {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	fmt.Println("listening on", *addr, "(a delivery of type webhook.test is only a test and never real money)")
	if err := http.ListenAndServe(*addr, nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func verify(args []string) {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	secret := fs.String("secret", "", "endpoint secret, including whsec_")
	url := fs.String("url", "", "the endpoint URL exactly as registered")
	ts := fs.String("timestamp", "", "X-RigePay-Timestamp")
	reqID := fs.String("request-id", "", "X-RigePay-Request-ID")
	sig := fs.String("signature", "", "X-RigePay-Signature")
	bodyFile := fs.String("body-file", "", "file holding the exact request body")
	ignoreAge := fs.Bool("ignore-age", true, "do not reject old timestamps (useful for saved deliveries)")
	_ = fs.Parse(args)
	body, err := os.ReadFile(*bodyFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	h := http.Header{}
	h.Set("X-RigePay-Timestamp", *ts)
	h.Set("X-RigePay-Request-ID", *reqID)
	h.Set("X-RigePay-Signature", *sig)
	tolerance := 5 * time.Minute
	now := time.Now()
	if *ignoreAge {
		var unix int64
		fmt.Sscan(*ts, &unix)
		now = time.Unix(unix, 0)
	}
	if rigepay.VerifyWebhook(*secret, *url, h, body, tolerance, now) {
		fmt.Println("valid")
		return
	}
	fmt.Println("INVALID: check the secret (whole string including whsec_), the URL exactly as registered (query string and trailing slash included) and that the body is the raw bytes received")
	os.Exit(1)
}
