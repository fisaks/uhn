package ihcsim

import (
	"fmt"
	"strings"

	"github.com/fisaks/uhn/internal/ihc"
)

// SOAP XML response builders matching real IHC controller format.
// The client parses these using token-level XML parsing with namespace-qualified types.

const soapEnvOpen = `<?xml version='1.0' encoding='UTF-8'?>` +
	`<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/" ` +
	`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" ` +
	`xmlns:xsd="http://www.w3.org/2001/XMLSchema">` +
	`<SOAP-ENV:Body>`

const soapEnvClose = `</SOAP-ENV:Body></SOAP-ENV:Envelope>`

// AuthenticateSuccessXML returns SOAP response for successful authentication.
func AuthenticateSuccessXML() string {
	return soapEnvOpen +
		`<ns1:authenticate2 xmlns:ns1="utcs" xsi:type="ns1:WSLoginResult">` +
		`<ns1:loginWasSuccessful xsi:type="xsd:boolean">true</ns1:loginWasSuccessful>` +
		`</ns1:authenticate2>` +
		soapEnvClose
}

// AuthenticateFailureXML returns SOAP response for failed authentication.
func AuthenticateFailureXML() string {
	return soapEnvOpen +
		`<ns1:authenticate2 xmlns:ns1="utcs" xsi:type="ns1:WSLoginResult">` +
		`<ns1:loginWasSuccessful xsi:type="xsd:boolean">false</ns1:loginWasSuccessful>` +
		`</ns1:authenticate2>` +
		soapEnvClose
}

// DisconnectSuccessXML returns SOAP response for successful disconnect.
func DisconnectSuccessXML() string {
	return `<?xml version='1.0' encoding='UTF-8'?>` +
		`<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/">` +
		`<SOAP-ENV:Body>` +
		`<ns1:disconnect1 xmlns:ns1="utcs">true</ns1:disconnect1>` +
		`</SOAP-ENV:Body></SOAP-ENV:Envelope>`
}

// EnableNotificationsXML returns SOAP response for enableRuntimeValueNotifications.
func EnableNotificationsXML() string {
	return soapEnvOpen +
		`<ns1:enableRuntimeValueNotifications2 xmlns:ns1="utcs">` +
		`<ns1:arrayItem xsi:nil="true" xsi:type="ns1:WSResourceValueEnvelope">` +
		`</ns1:arrayItem>` +
		`</ns1:enableRuntimeValueNotifications2>` +
		soapEnvClose
}

// WaitForChangesXML returns SOAP response for waitForResourceValueChanges
// with the given resource value envelopes.
func WaitForChangesXML(envelopes []ihc.ResourceValueEnvelope) string {
	var sb strings.Builder
	sb.WriteString(soapEnvOpen)
	sb.WriteString(`<ns1:waitForResourceValueChanges2 xmlns:ns1="utcs">`)

	for _, env := range envelopes {
		sb.WriteString(resourceValueEnvelopeXML(env))
	}

	sb.WriteString(`</ns1:waitForResourceValueChanges2>`)
	sb.WriteString(soapEnvClose)
	return sb.String()
}

// SetResourceValueSuccessXML returns SOAP response for successful setResourceValue.
func SetResourceValueSuccessXML() string {
	return soapEnvOpen +
		`<ns1:setResourceValue2 xmlns:ns1="utcs" xsi:type="xsd:boolean">true</ns1:setResourceValue2>` +
		soapEnvClose
}

// SOAPFaultXML returns a SOAP fault response.
func SOAPFaultXML(faultCode, faultString string) string {
	return `<?xml version='1.0' encoding='UTF-8'?>` +
		`<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/">` +
		`<SOAP-ENV:Body>` +
		`<SOAP-ENV:Fault>` +
		`<faultcode>` + faultCode + `</faultcode>` +
		`<faultstring>` + faultString + `</faultstring>` +
		`</SOAP-ENV:Fault>` +
		`</SOAP-ENV:Body></SOAP-ENV:Envelope>`
}

// SessionExpiredFaultXML returns the "Logon Failed" SOAP fault.
func SessionExpiredFaultXML() string {
	return SOAPFaultXML("SOAP-ENV:Client", "Logon Failed")
}

// resourceValueEnvelopeXML renders one arrayItem element.
func resourceValueEnvelopeXML(env ihc.ResourceValueEnvelope) string {
	var valueXML string
	switch {
	case env.Value.Bool != nil:
		v := "false"
		if *env.Value.Bool {
			v = "true"
		}
		valueXML = `<ns1:value xmlns:ns2="utcs.values" xsi:type="ns2:WSBooleanValue">` +
			`<ns2:value xsi:type="xsd:boolean">` + v + `</ns2:value>` +
			`</ns1:value>`
	case env.Value.Int != nil:
		valueXML = fmt.Sprintf(
			`<ns1:value xmlns:ns2="utcs.values" xsi:type="ns2:WSIntegerValue">`+
				`<ns2:integer xsi:type="xsd:int">%d</ns2:integer>`+
				`<ns2:maximumValue xsi:type="xsd:int">100</ns2:maximumValue>`+
				`<ns2:minimumValue xsi:type="xsd:int">0</ns2:minimumValue>`+
				`</ns1:value>`, *env.Value.Int)
	case env.Value.Float != nil:
		valueXML = fmt.Sprintf(
			`<ns1:value xmlns:ns2="utcs.values" xsi:type="ns2:WSFloatingPointValue">`+
				`<ns2:floatingPointValue xsi:type="xsd:double">%f</ns2:floatingPointValue>`+
				`</ns1:value>`, *env.Value.Float)
	}

	return fmt.Sprintf(
		`<ns1:arrayItem xsi:type="ns1:WSResourceValueEnvelope">`+
			`%s`+
			`<ns1:typeString xsi:type="xsd:string"></ns1:typeString>`+
			`<ns1:resourceID xsi:type="xsd:int">%d</ns1:resourceID>`+
			`<ns1:isValueRuntime xsi:type="xsd:boolean">true</ns1:isValueRuntime>`+
			`</ns1:arrayItem>`,
		valueXML, env.ResourceID)
}
