package ihc

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SOAPClient talks to a single IHC controller over HTTP SOAP.
// It is safe for concurrent use — Go's http.Client uses connection pooling,
// so the long-poll (WaitForResourceValueChanges) and commands (SetResourceValue)
// run on separate pooled connections concurrently. The JSESSIONID cookie is only
// written during Authenticate (single-caller) and read-only afterwards.
type SOAPClient struct {
	client  *http.Client
	baseURL string
	cookie  string // JSESSIONID
}

// NewSOAPClient creates a client for the given IHC controller.
func NewSOAPClient(host string, port int) *SOAPClient {
	return &SOAPClient{
		client: &http.Client{
			// No http.Client.Timeout — each doSOAP call sets its own
			// context.WithTimeout (5s for commands, configurable for long-poll).
			// DisableKeepAlives: IHC controllers close connections between
			// requests (especially across different SOAP services). The Java
			// reference implementation creates a new connection per request.
			Transport: &http.Transport{
				DisableKeepAlives: true,
			},
		},
		baseURL: fmt.Sprintf("http://%s:%d", host, port),
	}
}

// Authenticate logs in and stores the session cookie.
func (c *SOAPClient) Authenticate(ctx context.Context, username, password string) (bool, error) {
	body := fmt.Sprintf(
		`<?xml version="1.0" encoding="utf-8"?>`+
			`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" `+
			`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:tns="utcs">`+
			`<soap:Body>`+
			`<authenticate1 xmlns="utcs">`+
			`<username>%s</username>`+
			`<password>%s</password>`+
			`<application>treeview</application>`+
			`</authenticate1>`+
			`</soap:Body></soap:Envelope>`,
		xmlEscape(username), xmlEscape(password),
	)

	respBody, _, _, err := c.doSOAP(ctx, "/ws/AuthenticationService", "authenticate", body, 5*time.Second)
	if err != nil {
		return false, fmt.Errorf("authenticate: %w", err)
	}

	if err := checkSOAPFault(respBody); err != nil {
		return false, err
	}

	return strings.Contains(string(respBody), ">true</ns1:loginWasSuccessful>"), nil
}

// EnableRuntimeValueNotifications registers interest in the given resource IDs.
func (c *SOAPClient) EnableRuntimeValueNotifications(ctx context.Context, resourceIDs []int) error {
	var items strings.Builder
	for _, id := range resourceIDs {
		fmt.Fprintf(&items, "<arrayItem>%d</arrayItem>", id)
	}

	body := fmt.Sprintf(
		`<?xml version="1.0" encoding="utf-8"?>`+
			`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" `+
			`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" `+
			`xmlns:wpNS1="utcs.values" xmlns:tns="utcs">`+
			`<soap:Body>`+
			`<enableRuntimeValueNotifications1 xmlns="utcs">%s</enableRuntimeValueNotifications1>`+
			`</soap:Body></soap:Envelope>`,
		items.String(),
	)

	respBody, _, _, err := c.doSOAP(ctx, "/ws/ResourceInteractionService", "enableRuntimeValueNotifications", body, 5*time.Second)
	if err != nil {
		return fmt.Errorf("enableRuntimeValueNotifications: %w", err)
	}

	if err := checkSOAPFault(respBody); err != nil {
		return err
	}
	if !strings.Contains(string(respBody), "enableRuntimeValueNotifications2") {
		return fmt.Errorf("enableRuntimeValueNotifications: unexpected response")
	}
	return nil
}

// WaitForResourceValueChanges long-polls for value changes.
// Returns nil slice on timeout (no changes). Returns ErrSessionExpired on auth failure.
func (c *SOAPClient) WaitForResourceValueChanges(ctx context.Context, timeoutSec int) ([]ResourceValueEnvelope, error) {
	body := fmt.Sprintf(
		`<?xml version="1.0" encoding="utf-8"?>`+
			`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" `+
			`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" `+
			`xmlns:wpNS1="utcs.values" xmlns:tns="utcs">`+
			`<soap:Body>`+
			`<waitForResourceValueChanges1 xmlns="utcs">%d</waitForResourceValueChanges1>`+
			`</soap:Body></soap:Envelope>`,
		timeoutSec,
	)

	// HTTP timeout must exceed the IHC wait timeout
	httpTimeout := time.Duration(timeoutSec+5) * time.Second
	respBody, _, _, err := c.doSOAP(ctx, "/ws/ResourceInteractionService", "waitForResourceValueChanges", body, httpTimeout)
	if err != nil {
		return nil, fmt.Errorf("waitForResourceValueChanges: %w", err)
	}

	if err := checkSOAPFault(respBody); err != nil {
		return nil, err
	}

	if !strings.Contains(string(respBody), "waitForResourceValueChanges2") {
		return nil, fmt.Errorf("waitForResourceValueChanges: unexpected response")
	}

	return parseResourceValueEnvelopes(respBody)
}

// SetResourceValue writes a value to a resource on the controller.
func (c *SOAPClient) SetResourceValue(ctx context.Context, resourceID int, value IHCValue) (bool, error) {
	var valueXML string
	switch {
	case value.Bool != nil:
		v := "false"
		if *value.Bool {
			v = "true"
		}
		valueXML = fmt.Sprintf(`<value xsi:type="WSBooleanValue"><value xsi:type="xsd:boolean">%s</value></value>`, v)
	case value.Int != nil:
		valueXML = fmt.Sprintf(`<value xsi:type="WSIntegerValue"><integer xsi:type="xsd:int">%d</integer></value>`, *value.Int)
	case value.Float != nil:
		valueXML = fmt.Sprintf(`<value xsi:type="WSFloatingPointValue"><floatingPointValue xsi:type="xsd:double">%f</floatingPointValue></value>`, *value.Float)
	default:
		return false, fmt.Errorf("setResourceValue: empty IHCValue")
	}

	body := fmt.Sprintf(
		`<?xml version="1.0" encoding="utf-8"?>`+
			`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" `+
			`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" `+
			`xmlns:wpNS1="utcs.values" xmlns:tns="utcs">`+
			`<soap:Body>`+
			`<setResourceValue1 xmlns="utcs">`+
			`<resourceID>%d</resourceID>`+
			`<isValueRuntime>true</isValueRuntime>`+
			`%s`+
			`</setResourceValue1>`+
			`</soap:Body></soap:Envelope>`,
		resourceID, valueXML,
	)

	respBody, _, _, err := c.doSOAP(ctx, "/ws/ResourceInteractionService", "setResourceValue", body, 5*time.Second)
	if err != nil {
		return false, fmt.Errorf("setResourceValue: %w", err)
	}

	if err := checkSOAPFault(respBody); err != nil {
		return false, err
	}

	return strings.Contains(string(respBody), ">true</ns1:setResourceValue2>"), nil
}

// Disconnect ends the session. Always clears the cookie regardless of outcome.
func (c *SOAPClient) Disconnect(ctx context.Context) error {
	defer func() { c.cookie = "" }()

	body := `<?xml version="1.0" encoding="utf-8"?>` +
		`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" ` +
		`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:tns="utcs">` +
		`<soap:Body></soap:Body></soap:Envelope>`

	respBody, _, _, err := c.doSOAP(ctx, "/ws/AuthenticationService", "disconnect", body, 5*time.Second)
	if err != nil {
		return fmt.Errorf("disconnect: %w", err)
	}
	if err := checkSOAPFault(respBody); err != nil {
		return fmt.Errorf("disconnect: %w", err)
	}
	if !strings.Contains(string(respBody), ">true</ns1:disconnect1>") {
		return fmt.Errorf("disconnect: unexpected response")
	}
	return nil
}

// doSOAP sends a SOAP request with the session cookie and SOAPAction header.
// Returns the full response body (already read) so callers don't need to worry
// about the request context lifetime.
func (c *SOAPClient) doSOAP(ctx context.Context, path, action, body string, timeout time.Duration) ([]byte, int, []*http.Cookie, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewBufferString(body))
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", `"`+action+`"`)
	if c.cookie != "" {
		req.Header.Set("Cookie", "JSESSIONID="+c.cookie)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Update cookie if controller sends a new one
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "JSESSIONID" {
			c.cookie = cookie.Value
		}
	}

	return respBody, resp.StatusCode, resp.Cookies(), nil
}

// checkSOAPFault checks for SOAP faults in the response body.
// Returns ErrSessionExpired for "Logon Failed" faults.
func checkSOAPFault(body []byte) error {
	s := string(body)
	if !strings.Contains(s, "Fault") {
		return nil
	}
	if strings.Contains(s, "Logon Failed") {
		return ErrSessionExpired
	}
	// Extract faultstring if possible
	start := strings.Index(s, "<faultstring>")
	end := strings.Index(s, "</faultstring>")
	if start >= 0 && end > start {
		return fmt.Errorf("SOAP fault: %s", s[start+len("<faultstring>"):end])
	}
	return fmt.Errorf("SOAP fault in response")
}

// parseResourceValueEnvelopes parses the arrayItem elements from a
// waitForResourceValueChanges or getRuntimeValues response.
// Uses token-level XML parsing to handle xsi:type polymorphism.
func parseResourceValueEnvelopes(body []byte) ([]ResourceValueEnvelope, error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	var results []ResourceValueEnvelope

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("XML parse error: %w", err)
		}

		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != "arrayItem" {
			continue
		}

		// Check for xsi:nil="true" (empty response from enableRuntimeValueNotifications)
		if isNil(se) {
			skipElement(decoder)
			continue
		}

		env, err := parseOneEnvelope(decoder)
		if err != nil {
			return nil, err
		}
		results = append(results, env)
	}

	return results, nil
}

// parseOneEnvelope parses the children of a single <arrayItem> element.
func parseOneEnvelope(decoder *xml.Decoder) (ResourceValueEnvelope, error) {
	var env ResourceValueEnvelope
	depth := 1

	for depth > 0 {
		tok, err := decoder.Token()
		if err != nil {
			return env, fmt.Errorf("unexpected end of envelope: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "value":
				// This is the typed value element — check xsi:type
				xsiType := getAttr(t, "type")
				val, err := parseTypedValue(decoder, xsiType)
				if err != nil {
					return env, err
				}
				env.Value = val
				depth-- // parseTypedValue consumed the end element
			case "typeString":
				env.TypeString, _ = readCharData(decoder)
				depth-- // readCharData consumed the end element
			case "resourceID":
				text, _ := readCharData(decoder)
				fmt.Sscanf(text, "%d", &env.ResourceID)
				depth--
			case "isValueRuntime":
				text, _ := readCharData(decoder)
				env.IsValueRuntime = text == "true"
				depth--
			default:
				skipElement(decoder)
				depth--
			}
		case xml.EndElement:
			depth--
		}
	}

	return env, nil
}

// parseTypedValue parses a typed value based on xsi:type.
// The xsiType looks like "ns2:WSBooleanValue", "ns2:WSIntegerValue", "ns2:WSFloatingPointValue".
func parseTypedValue(decoder *xml.Decoder, xsiType string) (IHCValue, error) {
	var val IHCValue
	// Extract the type name after the colon
	typeName := xsiType
	if idx := strings.LastIndex(xsiType, ":"); idx >= 0 {
		typeName = xsiType[idx+1:]
	}

	depth := 1
	for depth > 0 {
		tok, err := decoder.Token()
		if err != nil {
			return val, fmt.Errorf("unexpected end of value: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch {
			case typeName == "WSBooleanValue" && t.Name.Local == "value":
				text, _ := readCharData(decoder)
				b := text == "true"
				val.Bool = &b
				depth--
			case typeName == "WSIntegerValue" && t.Name.Local == "integer":
				text, _ := readCharData(decoder)
				var i int
				fmt.Sscanf(text, "%d", &i)
				val.Int = &i
				depth--
			case typeName == "WSFloatingPointValue" && t.Name.Local == "floatingPointValue":
				text, _ := readCharData(decoder)
				var f float64
				fmt.Sscanf(text, "%f", &f)
				val.Float = &f
				depth--
			default:
				// Skip unknown children (maximumValue, minimumValue, etc.)
				skipElement(decoder)
				depth--
			}
		case xml.EndElement:
			depth--
		}
	}

	return val, nil
}

// readCharData reads text content up to and including the end element.
func readCharData(decoder *xml.Decoder) (string, error) {
	var sb strings.Builder
	for {
		tok, err := decoder.Token()
		if err != nil {
			return sb.String(), err
		}
		switch t := tok.(type) {
		case xml.CharData:
			sb.Write(t)
		case xml.EndElement:
			return strings.TrimSpace(sb.String()), nil
		}
	}
}

// skipElement skips all children until the matching end element.
func skipElement(decoder *xml.Decoder) {
	depth := 1
	for depth > 0 {
		tok, err := decoder.Token()
		if err != nil {
			return
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
}

// getAttr returns the value of a namespaced attribute by local name.
func getAttr(se xml.StartElement, localName string) string {
	for _, a := range se.Attr {
		if a.Name.Local == localName {
			return a.Value
		}
	}
	return ""
}

// isNil checks if an element has xsi:nil="true".
func isNil(se xml.StartElement) bool {
	return getAttr(se, "nil") == "true"
}

// xmlEscape escapes XML special characters in a string.
func xmlEscape(s string) string {
	var buf bytes.Buffer
	xml.EscapeText(&buf, []byte(s))
	return buf.String()
}
