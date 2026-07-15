package tools

import (
	"encoding/json"
	"regexp"
	"testing"
)

func callMap(t *testing.T, handler func(json.RawMessage) (interface{}, error), payload json.RawMessage) map[string]interface{} {
	t.Helper()
	value, err := handler(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result, ok := value.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", value)
	}
	return result
}

func TestUtilityToolsAreRegistered(t *testing.T) {
	expected := []string{"json_validate", "json_extract", "csv_to_json", "text_analyze", "hash_generate", "base64_convert", "timestamp_convert", "uuid_generate", "url_parse", "jwt_decode"}
	if len(All()) != 15 {
		t.Fatalf("expected 15 tools, got %d", len(All()))
	}
	for _, name := range expected {
		tool, err := ByName(name)
		if err != nil {
			t.Fatalf("missing tool %s: %v", name, err)
		}
		if tool.SatsPrice < 1 {
			t.Fatalf("tool %s has invalid price", name)
		}
	}
}

func TestJSONValidate(t *testing.T) {
	valid := callMap(t, HandleJSONValidate, json.RawMessage(`{"json":"{\"ok\":true}"}`))
	if valid["valid"] != true || valid["root_type"] != "object" {
		t.Fatalf("unexpected valid result: %#v", valid)
	}
	invalid := callMap(t, HandleJSONValidate, json.RawMessage(`{"json":"{"}`))
	if invalid["valid"] != false {
		t.Fatalf("expected invalid JSON result: %#v", invalid)
	}
}

func TestJSONExtract(t *testing.T) {
	result := callMap(t, HandleJSONExtract, json.RawMessage(`{"json":"{\"users\":[{\"email\":\"a@example.com\"}]}","path":"users[0].email"}`))
	if result["found"] != true || result["value"] != "a@example.com" {
		t.Fatalf("unexpected extract result: %#v", result)
	}
}

func TestCSVToJSON(t *testing.T) {
	result := callMap(t, HandleCSVToJSON, json.RawMessage(`{"csv":"name,age\nAda,36\nLinus,55"}`))
	if result["row_count"] != 2 {
		t.Fatalf("unexpected CSV result: %#v", result)
	}
}

func TestTextAnalyzeAndHash(t *testing.T) {
	analysis := callMap(t, HandleTextAnalyze, json.RawMessage(`{"text":"Hello world. Another sentence!"}`))
	if analysis["words"] != 4 || analysis["sentences"] != 2 {
		t.Fatalf("unexpected text analysis: %#v", analysis)
	}
	hash := callMap(t, HandleHashGenerate, json.RawMessage(`{"text":"abc","algorithm":"sha256","encoding":"hex"}`))
	if hash["digest"] != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("unexpected hash: %#v", hash)
	}
}

func TestBase64RoundTrip(t *testing.T) {
	encoded := callMap(t, HandleBase64Convert, json.RawMessage(`{"data":"LoopXXI","action":"encode"}`))
	decodedPayload, _ := json.Marshal(map[string]interface{}{"data": encoded["base64"], "action": "decode"})
	decoded := callMap(t, HandleBase64Convert, decodedPayload)
	if decoded["text"] != "LoopXXI" {
		t.Fatalf("unexpected Base64 result: %#v", decoded)
	}
}

func TestTimestampConvert(t *testing.T) {
	result := callMap(t, HandleTimestampConvert, json.RawMessage(`{"value":"0","from":"unix"}`))
	if result["rfc3339"] != "1970-01-01T00:00:00Z" {
		t.Fatalf("unexpected timestamp result: %#v", result)
	}
}

func TestUUIDGenerate(t *testing.T) {
	result := callMap(t, HandleUUIDGenerate, json.RawMessage(`{"count":3}`))
	values := result["uuids"].([]string)
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if len(values) != 3 {
		t.Fatalf("expected 3 UUIDs")
	}
	for _, value := range values {
		if !pattern.MatchString(value) {
			t.Fatalf("invalid UUID: %s", value)
		}
	}
}

func TestURLParse(t *testing.T) {
	result := callMap(t, HandleURLParse, json.RawMessage(`{"url":"https://example.com:443/a%20b?q=1#top"}`))
	if result["hostname"] != "example.com" || result["port"] != "443" || result["path"] != "/a%20b" {
		t.Fatalf("unexpected URL result: %#v", result)
	}
}

func TestJWTDecodeNeverClaimsVerification(t *testing.T) {
	result := callMap(t, HandleJWTDecode, json.RawMessage(`{"token":"eyJhbGciOiJub25lIn0.eyJzdWIiOiIxMjMifQ."}`))
	if result["verified"] != false {
		t.Fatalf("JWT decoder must never claim verification: %#v", result)
	}
	payload := result["payload"].(map[string]interface{})
	if payload["sub"] != "123" {
		t.Fatalf("unexpected JWT payload: %#v", payload)
	}
}
