package tools

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxUtilityInputBytes = 100_000

func utilityInputTooLarge(value, label string) error {
	if len(value) > maxUtilityInputBytes {
		return fmt.Errorf("%s exceeds %d bytes", label, maxUtilityInputBytes)
	}
	return nil
}

func jsonValueType(value interface{}) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case json.Number, float64:
		return "number"
	case string:
		return "string"
	case []interface{}:
		return "array"
	case map[string]interface{}:
		return "object"
	default:
		return "unknown"
	}
}

func decodeJSONValue(raw string) (interface{}, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value interface{}
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values are not allowed")
		}
		return nil, err
	}
	return value, nil
}

func jsonValidateTool() Tool {
	return Tool{
		Name:        "json_validate",
		Description: "Validate JSON, identify its root type, and return normalized JSON plus a SHA-256 content hash.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"json":{"type":"string","description":"JSON text to validate, up to 100 KB."}},"required":["json"]}`),
		Annotations: readOnlyAnnotations("JSON Validate"),
		SatsPrice:   5,
	}
}

func HandleJSONValidate(params json.RawMessage) (interface{}, error) {
	var input struct {
		JSON string `json:"json"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := utilityInputTooLarge(input.JSON, "json"); err != nil {
		return nil, err
	}
	value, err := decodeJSONValue(input.JSON)
	if err != nil {
		return map[string]interface{}{"valid": false, "error": err.Error()}, nil
	}
	normalized, _ := json.Marshal(value)
	hash := sha256.Sum256(normalized)
	return map[string]interface{}{
		"valid":           true,
		"root_type":       jsonValueType(value),
		"normalized_json": string(normalized),
		"bytes":           len(normalized),
		"sha256":          hex.EncodeToString(hash[:]),
	}, nil
}

func jsonExtractTool() Tool {
	return Tool{
		Name:        "json_extract",
		Description: "Extract one value from JSON using a simple dot path with array indexes, for example users[0].email.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"json":{"type":"string","description":"JSON text, up to 100 KB."},"path":{"type":"string","description":"Dot path such as user.profile.name or users[0].email."}},"required":["json","path"]}`),
		Annotations: readOnlyAnnotations("JSON Extract"),
		SatsPrice:   5,
	}
}

var arrayPathPart = regexp.MustCompile(`\[(\d+)\]`)

func HandleJSONExtract(params json.RawMessage) (interface{}, error) {
	var input struct {
		JSON string `json:"json"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := utilityInputTooLarge(input.JSON, "json"); err != nil {
		return nil, err
	}
	if len(input.Path) > 500 {
		return nil, fmt.Errorf("path exceeds 500 characters")
	}
	value, err := decodeJSONValue(input.JSON)
	if err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	path := strings.TrimSpace(input.Path)
	path = strings.TrimPrefix(path, "$")
	path = strings.TrimPrefix(path, ".")
	path = arrayPathPart.ReplaceAllString(path, ".$1")
	if path == "" {
		return map[string]interface{}{"found": true, "path": input.Path, "value": value, "value_type": jsonValueType(value)}, nil
	}
	current := value
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			continue
		}
		switch typed := current.(type) {
		case map[string]interface{}:
			next, ok := typed[part]
			if !ok {
				return map[string]interface{}{"found": false, "path": input.Path, "missing_at": part}, nil
			}
			current = next
		case []interface{}:
			index, convErr := strconv.Atoi(part)
			if convErr != nil || index < 0 || index >= len(typed) {
				return map[string]interface{}{"found": false, "path": input.Path, "missing_at": part}, nil
			}
			current = typed[index]
		default:
			return map[string]interface{}{"found": false, "path": input.Path, "missing_at": part}, nil
		}
	}
	return map[string]interface{}{"found": true, "path": input.Path, "value": current, "value_type": jsonValueType(current)}, nil
}

func csvToJSONTool() Tool {
	return Tool{
		Name:        "csv_to_json",
		Description: "Convert CSV, TSV, semicolon, or pipe-delimited text into structured JSON with bounded row output.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"csv":{"type":"string","description":"Delimited text, up to 100 KB."},"delimiter":{"type":"string","enum":["comma","tab","semicolon","pipe"],"description":"Delimiter name. Default comma."},"header":{"type":"boolean","description":"Use the first row as object keys. Default true."},"max_rows":{"type":"integer","minimum":1,"maximum":1000,"description":"Maximum data rows to return. Default 100."}},"required":["csv"]}`),
		Annotations: readOnlyAnnotations("CSV to JSON"),
		SatsPrice:   10,
	}
}

func HandleCSVToJSON(params json.RawMessage) (interface{}, error) {
	var input struct {
		CSV       string `json:"csv"`
		Delimiter string `json:"delimiter"`
		Header    *bool  `json:"header"`
		MaxRows   int    `json:"max_rows"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := utilityInputTooLarge(input.CSV, "csv"); err != nil {
		return nil, err
	}
	delimiter := ','
	switch input.Delimiter {
	case "", "comma":
	case "tab":
		delimiter = '\t'
	case "semicolon":
		delimiter = ';'
	case "pipe":
		delimiter = '|'
	default:
		return nil, fmt.Errorf("unsupported delimiter")
	}
	maxRows := input.MaxRows
	if maxRows == 0 {
		maxRows = 100
	}
	if maxRows < 1 || maxRows > 1000 {
		return nil, fmt.Errorf("max_rows must be between 1 and 1000")
	}
	hasHeader := true
	if input.Header != nil {
		hasHeader = *input.Header
	}
	reader := csv.NewReader(strings.NewReader(input.CSV))
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("CSV parse failed: %w", err)
	}
	if len(rows) == 0 {
		return map[string]interface{}{"records": []interface{}{}, "row_count": 0, "truncated": false}, nil
	}
	start := 0
	var headers []string
	if hasHeader {
		headers = rows[0]
		start = 1
	}
	available := len(rows) - start
	end := len(rows)
	if available > maxRows {
		end = start + maxRows
	}
	records := make([]interface{}, 0, end-start)
	for _, row := range rows[start:end] {
		if hasHeader {
			record := make(map[string]interface{}, len(headers))
			for index, header := range headers {
				key := strings.TrimSpace(header)
				if key == "" {
					key = fmt.Sprintf("column_%d", index+1)
				}
				value := ""
				if index < len(row) {
					value = row[index]
				}
				record[key] = value
			}
			records = append(records, record)
		} else {
			records = append(records, row)
		}
	}
	return map[string]interface{}{
		"records":          records,
		"row_count":        len(records),
		"source_row_count": available,
		"columns":          headers,
		"truncated":        available > maxRows,
		"delimiter":        input.Delimiter,
	}, nil
}

func textAnalyzeTool() Tool {
	return Tool{
		Name:        "text_analyze",
		Description: "Return deterministic text statistics, estimated tokens, reading time, and a SHA-256 content hash.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","description":"Text to analyze, up to 100 KB."}},"required":["text"]}`),
		Annotations: readOnlyAnnotations("Text Analyze"),
		SatsPrice:   5,
	}
}

var sentenceBreaks = regexp.MustCompile(`[.!?]+`)

func HandleTextAnalyze(params json.RawMessage) (interface{}, error) {
	var input struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := utilityInputTooLarge(input.Text, "text"); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(input.Text)
	words := len(strings.Fields(trimmed))
	lines := 0
	sentences := 0
	if trimmed != "" {
		lines = strings.Count(trimmed, "\n") + 1
		parts := sentenceBreaks.Split(trimmed, -1)
		for _, part := range parts {
			if strings.TrimSpace(part) != "" {
				sentences++
			}
		}
	}
	runes := utf8.RuneCountInString(input.Text)
	hash := sha256.Sum256([]byte(input.Text))
	return map[string]interface{}{
		"bytes":             len(input.Text),
		"characters":        runes,
		"words":             words,
		"lines":             lines,
		"sentences":         sentences,
		"estimated_tokens":  (runes + 3) / 4,
		"reading_minutes":   float64(words) / 200.0,
		"sha256":            hex.EncodeToString(hash[:]),
	}, nil
}

func hashGenerateTool() Tool {
	return Tool{
		Name:        "hash_generate",
		Description: "Generate a SHA-256 or SHA-512 digest in hexadecimal or Base64 form.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","description":"Input text, up to 100 KB."},"algorithm":{"type":"string","enum":["sha256","sha512"],"description":"Hash algorithm. Default sha256."},"encoding":{"type":"string","enum":["hex","base64"],"description":"Output encoding. Default hex."}},"required":["text"]}`),
		Annotations: readOnlyAnnotations("Hash Generate"),
		SatsPrice:   5,
	}
}

func HandleHashGenerate(params json.RawMessage) (interface{}, error) {
	var input struct {
		Text      string `json:"text"`
		Algorithm string `json:"algorithm"`
		Encoding  string `json:"encoding"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := utilityInputTooLarge(input.Text, "text"); err != nil {
		return nil, err
	}
	algorithm := input.Algorithm
	if algorithm == "" {
		algorithm = "sha256"
	}
	encoding := input.Encoding
	if encoding == "" {
		encoding = "hex"
	}
	var digest []byte
	switch algorithm {
	case "sha256":
		sum := sha256.Sum256([]byte(input.Text))
		digest = sum[:]
	case "sha512":
		sum := sha512.Sum512([]byte(input.Text))
		digest = sum[:]
	default:
		return nil, fmt.Errorf("unsupported algorithm")
	}
	var output string
	switch encoding {
	case "hex":
		output = hex.EncodeToString(digest)
	case "base64":
		output = base64.StdEncoding.EncodeToString(digest)
	default:
		return nil, fmt.Errorf("unsupported encoding")
	}
	return map[string]interface{}{"algorithm": algorithm, "encoding": encoding, "digest": output, "bytes": len(input.Text)}, nil
}

func base64ConvertTool() Tool {
	return Tool{
		Name:        "base64_convert",
		Description: "Encode UTF-8 text to Base64 or decode Base64 safely, with standard and URL-safe alphabets.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"data":{"type":"string","description":"Text or Base64 data, up to 100 KB."},"action":{"type":"string","enum":["encode","decode"]},"url_safe":{"type":"boolean","description":"Use the URL-safe alphabet without padding."}},"required":["data","action"]}`),
		Annotations: readOnlyAnnotations("Base64 Convert"),
		SatsPrice:   5,
	}
}

func HandleBase64Convert(params json.RawMessage) (interface{}, error) {
	var input struct {
		Data    string `json:"data"`
		Action  string `json:"action"`
		URLSafe bool   `json:"url_safe"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := utilityInputTooLarge(input.Data, "data"); err != nil {
		return nil, err
	}
	if input.Action == "encode" {
		encoding := base64.StdEncoding
		if input.URLSafe {
			return map[string]interface{}{"action": "encode", "url_safe": true, "base64": base64.RawURLEncoding.EncodeToString([]byte(input.Data)), "bytes": len(input.Data)}, nil
		}
		return map[string]interface{}{"action": "encode", "url_safe": false, "base64": encoding.EncodeToString([]byte(input.Data)), "bytes": len(input.Data)}, nil
	}
	if input.Action != "decode" {
		return nil, fmt.Errorf("action must be encode or decode")
	}
	var decoded []byte
	var err error
	if input.URLSafe {
		decoded, err = base64.RawURLEncoding.DecodeString(input.Data)
		if err != nil {
			decoded, err = base64.URLEncoding.DecodeString(input.Data)
		}
	} else {
		decoded, err = base64.StdEncoding.DecodeString(input.Data)
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(input.Data)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("invalid base64: %w", err)
	}
	result := map[string]interface{}{"action": "decode", "url_safe": input.URLSafe, "bytes": len(decoded), "base64": base64.StdEncoding.EncodeToString(decoded)}
	if utf8.Valid(decoded) {
		result["text"] = string(decoded)
	} else {
		result["text"] = nil
		result["hex"] = hex.EncodeToString(decoded)
	}
	return result, nil
}

func timestampConvertTool() Tool {
	return Tool{
		Name:        "timestamp_convert",
		Description: "Convert Unix seconds, Unix milliseconds, RFC3339, or ISO dates into normalized UTC timestamp forms.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string","description":"Timestamp value."},"from":{"type":"string","enum":["auto","unix","unix_ms","rfc3339","date"],"description":"Input format. Default auto."}},"required":["value"]}`),
		Annotations: readOnlyAnnotations("Timestamp Convert"),
		SatsPrice:   5,
	}
}

func HandleTimestampConvert(params json.RawMessage) (interface{}, error) {
	var input struct {
		Value string `json:"value"`
		From  string `json:"from"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	value := strings.TrimSpace(input.Value)
	format := input.From
	if format == "" || format == "auto" {
		if _, err := strconv.ParseInt(value, 10, 64); err == nil {
			if len(strings.TrimLeft(value, "-")) >= 13 {
				format = "unix_ms"
			} else {
				format = "unix"
			}
		} else if len(value) == 10 && strings.Count(value, "-") == 2 {
			format = "date"
		} else {
			format = "rfc3339"
		}
	}
	var parsed time.Time
	var err error
	switch format {
	case "unix":
		var seconds int64
		seconds, err = strconv.ParseInt(value, 10, 64)
		if err == nil {
			parsed = time.Unix(seconds, 0).UTC()
		}
	case "unix_ms":
		var milliseconds int64
		milliseconds, err = strconv.ParseInt(value, 10, 64)
		if err == nil {
			parsed = time.UnixMilli(milliseconds).UTC()
		}
	case "rfc3339":
		parsed, err = time.Parse(time.RFC3339Nano, value)
		parsed = parsed.UTC()
	case "date":
		parsed, err = time.ParseInLocation("2006-01-02", value, time.UTC)
	default:
		return nil, fmt.Errorf("unsupported input format")
	}
	if err != nil {
		return nil, fmt.Errorf("timestamp parse failed: %w", err)
	}
	return map[string]interface{}{
		"input_format": format,
		"rfc3339":      parsed.Format(time.RFC3339Nano),
		"unix":         parsed.Unix(),
		"unix_ms":      parsed.UnixMilli(),
		"date_utc":     parsed.Format("2006-01-02"),
		"time_utc":     parsed.Format("15:04:05"),
		"weekday":      parsed.Weekday().String(),
	}, nil
}

func uuidGenerateTool() Tool {
	return Tool{
		Name:        "uuid_generate",
		Description: "Generate one to one hundred cryptographically random RFC 4122 UUID version 4 identifiers.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer","minimum":1,"maximum":100,"description":"Number of UUIDs. Default 1."}},"required":[]}`),
		Annotations: readOnlyAnnotations("UUID Generate"),
		SatsPrice:   5,
	}
}

func HandleUUIDGenerate(params json.RawMessage) (interface{}, error) {
	var input struct {
		Count int `json:"count"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
	}
	if input.Count == 0 {
		input.Count = 1
	}
	if input.Count < 1 || input.Count > 100 {
		return nil, fmt.Errorf("count must be between 1 and 100")
	}
	values := make([]string, 0, input.Count)
	for index := 0; index < input.Count; index++ {
		bytes := make([]byte, 16)
		if _, err := rand.Read(bytes); err != nil {
			return nil, fmt.Errorf("random source failed: %w", err)
		}
		bytes[6] = (bytes[6] & 0x0f) | 0x40
		bytes[8] = (bytes[8] & 0x3f) | 0x80
		values = append(values, fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16]))
	}
	return map[string]interface{}{"version": 4, "count": len(values), "uuids": values}, nil
}

func urlParseTool() Tool {
	return Tool{
		Name:        "url_parse",
		Description: "Parse an HTTP or HTTPS URL into normalized scheme, host, port, path, query, and fragment fields without fetching it.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","description":"Absolute HTTP or HTTPS URL, up to 4,096 characters."}},"required":["url"]}`),
		Annotations: readOnlyAnnotations("URL Parse"),
		SatsPrice:   5,
	}
}

func HandleURLParse(params json.RawMessage) (interface{}, error) {
	var input struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if len(input.URL) > 4096 {
		return nil, fmt.Errorf("url exceeds 4096 characters")
	}
	parsed, err := url.Parse(strings.TrimSpace(input.URL))
	if err != nil {
		return nil, fmt.Errorf("URL parse failed: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("url scheme must be http or https")
	}
	if parsed.Hostname() == "" {
		return nil, fmt.Errorf("url must include a hostname")
	}
	query := make(map[string][]string)
	for key, values := range parsed.Query() {
		query[key] = values
	}
	return map[string]interface{}{
		"normalized": parsed.String(),
		"scheme":     parsed.Scheme,
		"host":       parsed.Host,
		"hostname":   parsed.Hostname(),
		"port":       parsed.Port(),
		"path":       parsed.EscapedPath(),
		"query":      query,
		"fragment":   parsed.Fragment,
		"has_userinfo": parsed.User != nil,
	}, nil
}

func jwtDecodeTool() Tool {
	return Tool{
		Name:        "jwt_decode",
		Description: "Decode JWT header and payload claims locally without verifying the signature. The response always marks verification as false.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"token":{"type":"string","description":"JWT compact serialization, up to 20 KB."}},"required":["token"]}`),
		Annotations: readOnlyAnnotations("JWT Decode"),
		SatsPrice:   5,
	}
}

func decodeJWTPart(part string) (map[string]interface{}, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		decoded, err = base64.URLEncoding.DecodeString(part)
	}
	if err != nil {
		return nil, err
	}
	var value map[string]interface{}
	decoder := json.NewDecoder(strings.NewReader(string(decoded)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func HandleJWTDecode(params json.RawMessage) (interface{}, error) {
	var input struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if len(input.Token) > 20_000 {
		return nil, fmt.Errorf("token exceeds 20000 characters")
	}
	parts := strings.Split(strings.TrimSpace(input.Token), ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("JWT must contain three dot-separated segments")
	}
	header, err := decodeJWTPart(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid JWT header: %w", err)
	}
	payload, err := decodeJWTPart(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid JWT payload: %w", err)
	}
	return map[string]interface{}{
		"header":            header,
		"payload":           payload,
		"signature_present": parts[2] != "",
		"verified":          false,
		"warning":           "Decoded only. Signature, issuer, audience, and expiry were not verified.",
	}, nil
}
