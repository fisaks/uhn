package ihc

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestParseWaitForResourceValueChanges_BooleanValues tests parsing the first
// waitForResourceValueChanges response (initial state) with two boolean resources.
// Real trace: 2246176-waitForResourceValueChanges.log
func TestParseWaitForResourceValueChanges_BooleanValues(t *testing.T) {
	xml := `<?xml version='1.0' encoding='UTF-8'?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
<SOAP-ENV:Body>
<ns1:waitForResourceValueChanges2 xmlns:ns1="utcs">
<ns1:arrayItem xsi:type="ns1:WSResourceValueEnvelope">
<ns1:value xmlns:ns2="utcs.values"  xsi:type="ns2:WSBooleanValue">
<ns2:value xsi:type="xsd:boolean">true</ns2:value>
</ns1:value>
<ns1:typeString xsi:type="xsd:string"></ns1:typeString>
<ns1:resourceID xsi:type="xsd:int">10422366</ns1:resourceID>
<ns1:isValueRuntime xsi:type="xsd:boolean">true</ns1:isValueRuntime>
</ns1:arrayItem>
<ns1:arrayItem xsi:type="ns1:WSResourceValueEnvelope">
<ns1:value xmlns:ns3="utcs.values"  xsi:type="ns3:WSBooleanValue">
<ns3:value xsi:type="xsd:boolean">false</ns3:value>
</ns1:value>
<ns1:typeString xsi:type="xsd:string"></ns1:typeString>
<ns1:resourceID xsi:type="xsd:int">10421340</ns1:resourceID>
<ns1:isValueRuntime xsi:type="xsd:boolean">true</ns1:isValueRuntime>
</ns1:arrayItem>
</ns1:waitForResourceValueChanges2>
</SOAP-ENV:Body>
</SOAP-ENV:Envelope>`

	envelopes, err := parseResourceValueEnvelopes([]byte(xml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(envelopes) != 2 {
		t.Fatalf("expected 2 envelopes, got %d", len(envelopes))
	}

	// First: boolean true, resourceID 10422366
	e := envelopes[0]
	if e.ResourceID != 10422366 {
		t.Errorf("envelope[0].ResourceID = %d, want 10422366", e.ResourceID)
	}
	if e.Value.Bool == nil || *e.Value.Bool != true {
		t.Errorf("envelope[0].Value.Bool = %v, want true", e.Value.Bool)
	}
	if !e.IsValueRuntime {
		t.Error("envelope[0].IsValueRuntime = false, want true")
	}

	// Second: boolean false, resourceID 10421340
	e = envelopes[1]
	if e.ResourceID != 10421340 {
		t.Errorf("envelope[1].ResourceID = %d, want 10421340", e.ResourceID)
	}
	if e.Value.Bool == nil || *e.Value.Bool != false {
		t.Errorf("envelope[1].Value.Bool = %v, want false", e.Value.Bool)
	}
}

// TestParseGetRuntimeValues_IntegerAndBoolean tests parsing getRuntimeValues
// with an integer (airlink_dimming) and a boolean (airlink_dimmer_decrease).
// Real trace: 2352509-getRuntimeValues.log
func TestParseGetRuntimeValues_IntegerAndBoolean(t *testing.T) {
	xml := `<?xml version='1.0' encoding='UTF-8'?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
<SOAP-ENV:Body>
<ns1:getRuntimeValues2 xmlns:ns1="utcs">
<ns1:arrayItem xsi:type="ns1:WSResourceValueEnvelope">
<ns1:value xmlns:ns2="utcs.values"  xsi:type="ns2:WSIntegerValue">
<ns2:integer xsi:type="xsd:int">50</ns2:integer>
<ns2:maximumValue xsi:type="xsd:int">100</ns2:maximumValue>
<ns2:minimumValue xsi:type="xsd:int">0</ns2:minimumValue>
</ns1:value>
<ns1:typeString xsi:type="xsd:string">airlink_dimming</ns1:typeString>
<ns1:resourceID xsi:type="xsd:int">4106589</ns1:resourceID>
<ns1:isValueRuntime xsi:type="xsd:boolean">true</ns1:isValueRuntime>
</ns1:arrayItem>
<ns1:arrayItem xsi:type="ns1:WSResourceValueEnvelope">
<ns1:value xmlns:ns3="utcs.values"  xsi:type="ns3:WSBooleanValue">
<ns3:value xsi:type="xsd:boolean">false</ns3:value>
</ns1:value>
<ns1:typeString xsi:type="xsd:string">airlink_dimmer_decrease</ns1:typeString>
<ns1:resourceID xsi:type="xsd:int">4106336</ns1:resourceID>
<ns1:isValueRuntime xsi:type="xsd:boolean">true</ns1:isValueRuntime>
</ns1:arrayItem>
</ns1:getRuntimeValues2>
</SOAP-ENV:Body>
</SOAP-ENV:Envelope>`

	envelopes, err := parseResourceValueEnvelopes([]byte(xml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(envelopes) != 2 {
		t.Fatalf("expected 2 envelopes, got %d", len(envelopes))
	}

	// First: integer 50, airlink_dimming
	e := envelopes[0]
	if e.ResourceID != 4106589 {
		t.Errorf("envelope[0].ResourceID = %d, want 4106589", e.ResourceID)
	}
	if e.TypeString != "airlink_dimming" {
		t.Errorf("envelope[0].TypeString = %q, want %q", e.TypeString, "airlink_dimming")
	}
	if e.Value.Int == nil || *e.Value.Int != 50 {
		t.Errorf("envelope[0].Value.Int = %v, want 50", e.Value.Int)
	}

	// Second: boolean false, airlink_dimmer_decrease
	e = envelopes[1]
	if e.ResourceID != 4106336 {
		t.Errorf("envelope[1].ResourceID = %d, want 4106336", e.ResourceID)
	}
	if e.TypeString != "airlink_dimmer_decrease" {
		t.Errorf("envelope[1].TypeString = %q, want %q", e.TypeString, "airlink_dimmer_decrease")
	}
	if e.Value.Bool == nil || *e.Value.Bool != false {
		t.Errorf("envelope[1].Value.Bool = %v, want false", e.Value.Bool)
	}
}

// TestParseEnableNotificationsResponse tests that the nil arrayItem response
// from enableRuntimeValueNotifications is handled correctly.
func TestParseEnableNotificationsResponse(t *testing.T) {
	xml := `<?xml version='1.0' encoding='UTF-8'?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
<SOAP-ENV:Body>
<ns1:enableRuntimeValueNotifications2 xmlns:ns1="utcs">
<ns1:arrayItem xsi:nil="true" xsi:type="ns1:WSResourceValueEnvelope">
</ns1:arrayItem>
</ns1:enableRuntimeValueNotifications2>
</SOAP-ENV:Body>
</SOAP-ENV:Envelope>`

	envelopes, err := parseResourceValueEnvelopes([]byte(xml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(envelopes) != 0 {
		t.Errorf("expected 0 envelopes for nil response, got %d", len(envelopes))
	}
}

// TestCheckSOAPFault_SessionExpired tests SOAP fault detection for session expiry.
func TestCheckSOAPFault_SessionExpired(t *testing.T) {
	xml := `<?xml version='1.0' encoding='UTF-8'?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/">
<SOAP-ENV:Body>
<SOAP-ENV:Fault>
<faultcode>SOAP-ENV:Client</faultcode>
<faultstring>Logon Failed</faultstring>
</SOAP-ENV:Fault>
</SOAP-ENV:Body>
</SOAP-ENV:Envelope>`

	err := checkSOAPFault([]byte(xml))
	if err != ErrSessionExpired {
		t.Errorf("expected ErrSessionExpired, got %v", err)
	}
}

// TestCheckSOAPFault_NoFault tests that non-fault responses pass through.
func TestCheckSOAPFault_NoFault(t *testing.T) {
	xml := `<?xml version='1.0' encoding='UTF-8'?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/">
<SOAP-ENV:Body>
<ns1:setResourceValue2 xmlns:ns1="utcs" xsi:type="xsd:boolean">true</ns1:setResourceValue2>
</SOAP-ENV:Body>
</SOAP-ENV:Envelope>`

	err := checkSOAPFault([]byte(xml))
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

// TestCheckSOAPFault_OtherFault tests detection of non-session faults.
func TestCheckSOAPFault_OtherFault(t *testing.T) {
	xml := `<?xml version='1.0' encoding='UTF-8'?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/">
<SOAP-ENV:Body>
<SOAP-ENV:Fault>
<faultcode>SOAP-ENV:Server</faultcode>
<faultstring>Internal error</faultstring>
</SOAP-ENV:Fault>
</SOAP-ENV:Body>
</SOAP-ENV:Envelope>`

	err := checkSOAPFault([]byte(xml))
	if err == nil {
		t.Error("expected error, got nil")
	}
	if err == ErrSessionExpired {
		t.Error("expected non-session error, got ErrSessionExpired")
	}
	if !strings.Contains(err.Error(), "Internal error") {
		t.Errorf("expected error to contain 'Internal error', got %q", err.Error())
	}
}

// TestIHCValueToAny tests the IHCValue.ToAny() method.
func TestIHCValueToAny(t *testing.T) {
	b := BoolValue(true)
	if v, ok := b.ToAny().(bool); !ok || v != true {
		t.Errorf("BoolValue(true).ToAny() = %v, want true", b.ToAny())
	}

	i := IntValue(42)
	if v, ok := i.ToAny().(int); !ok || v != 42 {
		t.Errorf("IntValue(42).ToAny() = %v, want 42", i.ToAny())
	}

	f := FloatValue(22.5)
	if v, ok := f.ToAny().(float64); !ok || v != 22.5 {
		t.Errorf("FloatValue(22.5).ToAny() = %v, want 22.5", f.ToAny())
	}

	empty := IHCValue{}
	if empty.ToAny() != nil {
		t.Errorf("empty.ToAny() = %v, want nil", empty.ToAny())
	}
}

// TestAuthenticate_Integration tests the full authenticate SOAP roundtrip against a mock server.
func TestAuthenticate_Integration(t *testing.T) {
	// Mock IHC controller SOAP response (from real trace)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check request basics
		if r.URL.Path != "/ws/AuthenticationService" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("SOAPAction") != `"authenticate"` {
			t.Errorf("unexpected SOAPAction: %s", r.Header.Get("SOAPAction"))
		}

		// Verify request body contains credentials
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "<username>testuser</username>") {
			t.Error("request body missing username")
		}

		// Send response with JSESSIONID cookie
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "test-session-123"})
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.WriteHeader(200)
		w.Write([]byte(`<?xml version='1.0' encoding='UTF-8'?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
<SOAP-ENV:Body>
<ns1:authenticate2 xmlns:ns1="utcs" xsi:type="ns1:WSLoginResult">
<ns1:loginWasSuccessful xsi:type="xsd:boolean">true</ns1:loginWasSuccessful>
</ns1:authenticate2>
</SOAP-ENV:Body>
</SOAP-ENV:Envelope>`))
	}))
	defer server.Close()

	// Extract host:port from server URL
	addr := strings.TrimPrefix(server.URL, "http://")
	parts := strings.SplitN(addr, ":", 2)
	host := parts[0]

	client := &SOAPClient{
		client:  server.Client(),
		baseURL: "http://" + addr,
	}
	_ = host

	ok, err := client.Authenticate(context.Background(), "testuser", "testpass")
	if err != nil {
		t.Fatalf("authenticate error: %v", err)
	}
	if !ok {
		t.Error("expected login success")
	}
	if client.cookie != "test-session-123" {
		t.Errorf("cookie = %q, want %q", client.cookie, "test-session-123")
	}
}

// TestSetResourceValue_Boolean tests generating a boolean setResourceValue SOAP request.
func TestSetResourceValue_Boolean(t *testing.T) {
	var capturedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = string(body)

		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.WriteHeader(200)
		w.Write([]byte(`<?xml version='1.0' encoding='UTF-8'?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
<SOAP-ENV:Body>
<ns1:setResourceValue2 xmlns:ns1="utcs" xsi:type="xsd:boolean">true</ns1:setResourceValue2>
</SOAP-ENV:Body>
</SOAP-ENV:Envelope>`))
	}))
	defer server.Close()

	client := &SOAPClient{
		client:  server.Client(),
		baseURL: server.URL,
		cookie:  "test-session",
	}

	ok, err := client.SetResourceValue(context.Background(), 10422366, BoolValue(false))
	if err != nil {
		t.Fatalf("setResourceValue error: %v", err)
	}
	if !ok {
		t.Error("expected success")
	}

	// Verify SOAP body matches the wire format from traces
	if !strings.Contains(capturedBody, "<resourceID>10422366</resourceID>") {
		t.Error("request missing resourceID")
	}
	if !strings.Contains(capturedBody, `xsi:type="WSBooleanValue"`) {
		t.Error("request missing WSBooleanValue type")
	}
	if !strings.Contains(capturedBody, ">false</") {
		t.Error("request missing boolean value")
	}
}

// TestSetResourceValue_Integer tests generating an integer setResourceValue SOAP request (dimmer).
func TestSetResourceValue_Integer(t *testing.T) {
	var capturedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = string(body)

		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.WriteHeader(200)
		w.Write([]byte(`<?xml version='1.0' encoding='UTF-8'?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/">
<SOAP-ENV:Body>
<ns1:setResourceValue2 xmlns:ns1="utcs" xsi:type="xsd:boolean">true</ns1:setResourceValue2>
</SOAP-ENV:Body>
</SOAP-ENV:Envelope>`))
	}))
	defer server.Close()

	client := &SOAPClient{
		client:  server.Client(),
		baseURL: server.URL,
		cookie:  "test-session",
	}

	ok, err := client.SetResourceValue(context.Background(), 4106589, IntValue(50))
	if err != nil {
		t.Fatalf("setResourceValue error: %v", err)
	}
	if !ok {
		t.Error("expected success")
	}

	if !strings.Contains(capturedBody, "<resourceID>4106589</resourceID>") {
		t.Error("request missing resourceID")
	}
	if !strings.Contains(capturedBody, `xsi:type="WSIntegerValue"`) {
		t.Error("request missing WSIntegerValue type")
	}
	if !strings.Contains(capturedBody, ">50</") {
		t.Error("request missing integer value")
	}
}

// TestWaitForResourceValueChanges_SessionExpired tests that ErrSessionExpired is returned on SOAP fault.
func TestWaitForResourceValueChanges_SessionExpired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.WriteHeader(200)
		w.Write([]byte(`<?xml version='1.0' encoding='UTF-8'?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/">
<SOAP-ENV:Body>
<SOAP-ENV:Fault>
<faultcode>SOAP-ENV:Client</faultcode>
<faultstring>Logon Failed</faultstring>
</SOAP-ENV:Fault>
</SOAP-ENV:Body>
</SOAP-ENV:Envelope>`))
	}))
	defer server.Close()

	client := &SOAPClient{
		client:  server.Client(),
		baseURL: server.URL,
		cookie:  "expired-session",
	}

	_, err := client.WaitForResourceValueChanges(context.Background(), 10)
	if err != ErrSessionExpired {
		t.Errorf("expected ErrSessionExpired, got %v", err)
	}
}

// TestXmlEscape tests that XML special characters are properly escaped in credentials.
func TestXmlEscape(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"hello", "hello"},
		{"<>&\"'", "&lt;&gt;&amp;&#34;&#39;"},
		{"pass&word", "pass&amp;word"},
	}
	for _, tt := range tests {
		got := xmlEscape(tt.input)
		if got != tt.want {
			t.Errorf("xmlEscape(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
