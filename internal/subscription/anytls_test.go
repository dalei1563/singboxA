package subscription

import "testing"

func TestParseAnyTLSURI(t *testing.T) {
	parser := NewParser()

	proxies, err := parser.Parse([]byte("anytls://secret@example.com:443?sni=edge.example.com&insecure=1&alpn=h2,http/1.1&fp=chrome#AnyTLS%20Node"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(proxies) != 1 {
		t.Fatalf("expected 1 proxy, got %d", len(proxies))
	}

	proxy := proxies[0]
	if proxy.Type != "anytls" {
		t.Fatalf("expected type anytls, got %q", proxy.Type)
	}
	if proxy.Password != "secret" {
		t.Fatalf("expected password secret, got %q", proxy.Password)
	}
	if proxy.Server != "example.com" || proxy.Port != 443 {
		t.Fatalf("expected example.com:443, got %s:%d", proxy.Server, proxy.Port)
	}
	if proxy.ServerName != "edge.example.com" {
		t.Fatalf("expected server name edge.example.com, got %q", proxy.ServerName)
	}
	if !proxy.SkipCertVerify {
		t.Fatalf("expected insecure TLS to be enabled")
	}
	if len(proxy.ALPN) != 2 || proxy.ALPN[0] != "h2" || proxy.ALPN[1] != "http/1.1" {
		t.Fatalf("unexpected ALPN: %#v", proxy.ALPN)
	}
	if proxy.ClientFingerprint != "chrome" {
		t.Fatalf("expected fingerprint chrome, got %q", proxy.ClientFingerprint)
	}
	if proxy.Name != "AnyTLS Node" {
		t.Fatalf("expected decoded name, got %q", proxy.Name)
	}
}

func TestConvertAnyTLSProxy(t *testing.T) {
	converter := NewConverter()
	proxy := ClashProxy{
		Name:              "AnyTLS Node",
		Type:              "anytls",
		Server:            "example.com",
		Port:              443,
		Password:          "secret",
		ServerName:        "edge.example.com",
		SkipCertVerify:    true,
		ClientFingerprint: "chrome",
		ALPN:              []string{"h2", "http/1.1"},
	}

	outbounds := converter.Convert([]ClashProxy{proxy})
	if len(outbounds) != 1 {
		t.Fatalf("expected 1 outbound, got %d", len(outbounds))
	}

	outbound := outbounds[0]
	if outbound.Type != "anytls" {
		t.Fatalf("expected outbound type anytls, got %q", outbound.Type)
	}
	if outbound.Password != "secret" {
		t.Fatalf("expected password secret, got %q", outbound.Password)
	}
	if outbound.TLS == nil || !outbound.TLS.Enabled {
		t.Fatalf("expected TLS to be enabled")
	}
	if outbound.TLS.ServerName != "edge.example.com" {
		t.Fatalf("expected TLS server name edge.example.com, got %q", outbound.TLS.ServerName)
	}
	if !outbound.TLS.Insecure {
		t.Fatalf("expected TLS insecure to be true")
	}
	if outbound.TLS.UTLS == nil || outbound.TLS.UTLS.Fingerprint != "chrome" {
		t.Fatalf("expected uTLS fingerprint chrome, got %#v", outbound.TLS.UTLS)
	}
}
