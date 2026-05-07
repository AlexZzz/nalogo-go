package nalogo

import (
	"log/slog"
	"regexp"
)

// MaskedString is a string whose slog representation is always "***".
// Use it for INN, phone numbers, tokens, and passwords in log records.
type MaskedString string

func (m MaskedString) LogValue() slog.Value {
	return slog.StringValue("***")
}

var bodyMaskREs = []*regexp.Regexp{
	regexp.MustCompile(`("token"\s*:\s*")[^"]*(")`),
	regexp.MustCompile(`("refreshToken"\s*:\s*")[^"]*(")`),
	regexp.MustCompile(`("password"\s*:\s*")[^"]*(")`),
	regexp.MustCompile(`("secret"\s*:\s*")[^"]*(")`),
}

// sanitizeBody replaces sensitive JSON field values with "***".
func sanitizeBody(body string) string {
	if len(body) > 1000 {
		body = body[:1000]
	}
	for _, re := range bodyMaskREs {
		body = re.ReplaceAllString(body, `${1}***${2}`)
	}
	return body
}

var sensitiveHeaders = map[string]bool{
	"authorization": true,
	"x-api-key":     true,
	"cookie":        true,
	"set-cookie":    true,
}

// sanitizeHeaders returns a copy of headers with sensitive values replaced by "***".
func sanitizeHeaders(headers map[string]string) map[string]string {
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		if sensitiveHeaders[k] {
			out[k] = "***"
		} else {
			out[k] = v
		}
	}
	return out
}
