package contracts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"unicode"
)

// An upgrade step is Upgrade<Kind>(from []byte) ([]byte, error), one per version step, tested by the pairs in testdata/upgrade/<dir>/<from>-<to>/ (CT-RULE-3).

// reasonOf is the owner's mapping of an error/v1 code to a Problem's type and reason, the one mankit serve writes; a code not here stays the type, with no reason.
var reasonOf = map[string][2]string{
	"token_refused":     {"refused", "token"},
	"host_refused":      {"refused", "host"},
	"origin_refused":    {"refused", "origin"},
	"not_json":          {"refused", "not_json"},
	"unconfirmed":       {"refused", "unconfirmed"},
	"config_unreadable": {"failed", "config_unreadable"},
	"core_needs_values": {"failed", "core_needs_values"},
	// mankit's and the apps' other words, by the same pattern: failed when the request was fine and the app could not do it, refused when the request is not taken.
	"feature_needs_values":      {"failed", "feature_needs_values"},
	"queue_timeout":             {"failed", "queue_timeout"},
	"not_read_back":             {"failed", "not_read_back"},
	"ticket_lost":               {"failed", "ticket_lost"},
	"queue_unusable":            {"failed", "queue_unusable"},
	"queue_backend_unsupported": {"failed", "queue_backend_unsupported"},
	"change_too_long":           {"failed", "change_too_long"},
	"too_many_streams":          {"refused", "too_many_streams"},
	"too_large":                 {"refused", "too_large"},
}

type errorV1 struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	At      *struct {
		File  string `json:"file"`
		Field string `json:"field"`
	} `json:"at"`
}

const problemAPIVersion = "elderlygate/contracts/problem/v1"

type place struct {
	File string `json:"file,omitempty"`
	Path string `json:"path,omitempty"`
}

type problem struct {
	APIVersion string  `json:"apiVersion"`
	Kind       string  `json:"kind"`
	Type       string  `json:"type"`
	Detail     string  `json:"detail"`
	Reason     string  `json:"reason,omitempty"`
	At         []place `json:"at,omitempty"`
}

// UpgradeEnvelope is the step from envelope/v1 to envelope/v2: ran becomes source unchanged, and each error/v1 item becomes a Problem.
// A code becomes type and reason by the owner's mapping (any other code stays the type), message becomes detail, and at's file and field become one place's file and path.
// Dropped: at's kind and id, as a place has no field for them; at's file when problem/v1 refuses it as a machine path, rather than refusing the whole answer; at itself when nothing is left; and supported, as the version answer lists the versions.
// The app's own apiVersion, kind and data are untouched: they name its contract, not the envelope.
// It is Go only for now, as the readers that mix versions (servers, control-room) are Go; a TypeScript step comes only if a page needs one.
func UpgradeEnvelope(v1 []byte) ([]byte, error) {
	if err := Validate("envelope/v1/envelope.schema.json", v1); err != nil {
		return nil, fmt.Errorf("not an envelope/v1 answer: %w", err)
	}
	var in struct {
		APIVersion string            `json:"apiVersion"`
		Kind       string            `json:"kind"`
		Ran        json.RawMessage   `json:"ran"`
		Data       json.RawMessage   `json:"data"`
		Errors     []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(v1, &in); err != nil {
		return nil, err
	}
	out := struct {
		APIVersion string          `json:"apiVersion"`
		Kind       string          `json:"kind"`
		Source     json.RawMessage `json:"source"`
		Data       json.RawMessage `json:"data"`
		Errors     []problem       `json:"errors"`
	}{APIVersion: in.APIVersion, Kind: in.Kind, Source: in.Ran, Data: in.Data, Errors: []problem{}}
	for _, raw := range in.Errors {
		var e errorV1
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, err
		}
		p := problem{APIVersion: problemAPIVersion, Kind: "Problem", Type: e.Code, Detail: e.Message}
		if m, ok := reasonOf[e.Code]; ok {
			p.Type, p.Reason = m[0], m[1]
		}
		if e.At != nil {
			at := place{File: e.At.File, Path: e.At.Field}
			if at.File != "" && !takenFile(at.File) {
				at.File = ""
			}
			if at != (place{}) {
				p.At = []place{at}
			}
		}
		out.Errors = append(out.Errors, p)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	if err := Validate("envelope/v2/envelope.schema.json", buf.Bytes()); err != nil {
		return nil, fmt.Errorf("the upgrade is not an envelope/v2 answer: %w", err)
	}
	return buf.Bytes(), nil
}

// takenFile is whether problem/v1 takes file as a place's file, so the rule is problem/v1's alone.
func takenFile(file string) bool {
	doc, err := json.Marshal(problem{APIVersion: problemAPIVersion, Kind: "Problem", Type: "x", Detail: "x", At: []place{{File: file}}})
	return err == nil && Validate("problem/v1/problem.schema.json", doc) == nil
}

// UpgradeMankit is the step from mankit/v1 to mankit/v2 for an answer mankit gives every app: the envelope step, as the data is the same in both.
// The app's own apiVersion and contract are untouched, as with UpgradeEnvelope: the app's own step names its contract.
func UpgradeMankit(v1 []byte) ([]byte, error) {
	var head struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(v1, &head); err != nil {
		return nil, err
	}
	if !mankitAnswer(head.Kind) {
		return nil, fmt.Errorf("not an answer mankit gives every app: kind %q", head.Kind)
	}
	if err := Validate(MankitSchema("v1", head.Kind), v1); err != nil {
		return nil, fmt.Errorf("not a mankit/v1 %s: %w", head.Kind, err)
	}
	enveloped, err := UpgradeEnvelope(v1)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := json.Unmarshal(enveloped, &doc); err != nil {
		return nil, err
	}
	return encodeChecked(doc, MankitSchema("v2", head.Kind))
}

// The words mankit/v3's LicenseShow gives for v2's, by field: a state, a license's kind and where a feature is unlocked from.
var (
	licenseStateV3 = map[string]string{"none": "free", "trial": "trial", "trial-ended": "free", "licensed": "licensed", "ending": "licensed", "ended": "free"}
	licenseKindV3  = map[string]string{"org": "license", "product": "license", "release-trial": "trial"}
	licenseFromV3  = map[string]string{"org": "license", "product": "license", "both": "license", "trial": "trial", "none": "none"}
)

// UpgradeLicenseShow is the step from mankit/v2 to mankit/v3 for LicenseShow, the one answer mankit/v3 changes: each word becomes v3's (licenseStateV3, licenseKindV3, licenseFromV3), and a trial loses until and offerEnds, as its offer does not end (ADR-L9), so a trial not started also loses its daysLeft and has not ended, and v2's sentence for an ended offer is dropped, as v3 never says a trial offer ended.
// The licenses come first, then the trial, as v3 orders them. warn, endedOn and the rest mean the same in both, so they stay. A person's trial key has no v3 kind, so an answer that took one is refused, as is any word v3 has none for and a feature id v3 refuses (G-STACK-17).
// The app's own apiVersion and contract are untouched, as with UpgradeMankit: the app's own step names its contract.
func UpgradeLicenseShow(v2 []byte) ([]byte, error) {
	if err := Validate(MankitSchema("v2", "LicenseShow"), v2); err != nil {
		return nil, fmt.Errorf("not a mankit/v2 LicenseShow: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(v2, &doc); err != nil {
		return nil, err
	}
	data := doc["data"].(map[string]any)
	state, ok := licenseStateV3[data["state"].(string)]
	if !ok {
		return nil, fmt.Errorf("a %s state has no mankit/v3 word", data["state"])
	}
	data["state"] = state
	licenses := data["licenses"].([]any)
	for _, l := range licenses {
		license := l.(map[string]any)
		kind, ok := licenseKindV3[license["kind"].(string)]
		if !ok {
			return nil, fmt.Errorf("a %s license has no mankit/v3 kind", license["kind"])
		}
		license["kind"] = kind
		if kind == "trial" {
			delete(license, "until")
			delete(license, "offerEnds")
			if _, started := license["trialEnds"]; !started {
				if license["ended"] == true {
					delete(data, "sentence")
				}
				delete(license, "daysLeft")
				license["ended"] = false
			}
		}
	}
	sort.SliceStable(licenses, func(i, j int) bool {
		return licenses[i].(map[string]any)["kind"] == "license" && licenses[j].(map[string]any)["kind"] == "trial"
	})
	for _, f := range data["features"].([]any) {
		feature := f.(map[string]any)
		from, ok := licenseFromV3[feature["from"].(string)]
		if !ok {
			return nil, fmt.Errorf("a feature from %s has no mankit/v3 word", feature["from"])
		}
		feature["from"] = from
	}
	return encodeChecked(doc, MankitSchema("v3", "LicenseShow"))
}

const (
	designerV1   = "elderlygate/designer/app/v1"
	designerV2   = "elderlygate/designer/app/v2"
	designFormV2 = "elderlygate/designer/design/v2"
	designFormV3 = "elderlygate/designer/design/v3"
)

// UpgradeDesigner is the step from designer/app/v1 to v2: the envelope step, then the app's own form and contract, the design files' form inside the data (design/v2 to design/v3), and for Config the readings v2 keeps and the styles, colours and rules v1 never had, given empty.
// A v1 Version answer has no v2 kind: every app's version answer is contracts version/v1.
func UpgradeDesigner(v1 []byte) ([]byte, error) {
	var head struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
	}
	if err := json.Unmarshal(v1, &head); err != nil {
		return nil, err
	}
	if head.APIVersion != designerV1 {
		return nil, fmt.Errorf("not a designer/app/v1 answer: apiVersion is %q", head.APIVersion)
	}
	if head.Kind == "Version" {
		return nil, fmt.Errorf("designer/app/v2 has no Version: the version answer is contracts version/v1")
	}
	if err := Validate(DesignerSchema("v1", head.Kind), v1); err != nil {
		return nil, fmt.Errorf("not a designer/app/v1 %s: %w", head.Kind, err)
	}
	enveloped, err := UpgradeEnvelope(v1)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(enveloped, &doc); err != nil {
		return nil, err
	}
	doc["apiVersion"] = designerV2
	doc["source"].(map[string]any)["contract"] = "v2"
	doc["data"] = swapForm(doc["data"], designFormV2, designFormV3)
	switch head.Kind {
	case "Config":
		upConfig(doc["data"].(map[string]any))
	case "Board":
		upBoard(doc["data"].(map[string]any))
	}
	return encodeChecked(doc, DesignerSchema("v2", head.Kind))
}

// upConfig gives the readings designer/app/v2 has, its statuses read from the list the status property names in the definitions as v1 gave them, and the styles, colours and rules a v1 answer never said as empty, never guessed.
func upConfig(data map[string]any) {
	if readings, ok := data["readings"].(map[string]any); ok {
		readings["statuses"] = statusesOf(data["definitions"])
	}
	data["styles"] = map[string]any{}
	data["colors"] = map[string]any{}
	data["rules"] = []any{}
}

// upBoard gives what a v1 Board never said (each thing's style, the colours, the groups' members) as empty, never guessed.
func upBoard(data map[string]any) {
	data["styles"] = map[string]any{}
	data["colors"] = map[string]any{}
	data["groupMembers"] = map[string]any{}
}

// statusesOf is each status of the list the property at state.status names, in lower case and once, with what it means; empty when the definitions name none.
func statusesOf(definitions any) []any {
	out := []any{}
	defs, _ := definitions.(map[string]any)
	props, _ := defs["properties"].(map[string]any)
	// By property name in order, so two properties at state.status always give the same one.
	names := make([]string, 0, len(props))
	for n := range props {
		names = append(names, n)
	}
	sort.Strings(names)
	var name string
	for _, n := range names {
		if prop, _ := props[n].(map[string]any); prop["key"] == "state.status" {
			name, _ = prop["values"].(string)
			break
		}
	}
	vocabulary, _ := defs["vocabulary"].(map[string]any)
	list, _ := vocabulary[name].([]any)
	seen := map[string]bool{}
	for _, entry := range list {
		s := map[string]any{"settled": false}
		switch e := entry.(type) {
		case string:
			s["value"] = e
		case map[string]any:
			s["value"], _ = e["value"].(string)
			if e["settled"] == true {
				s["settled"] = true
			}
			if note, ok := e["note"].(string); ok && note != "" {
				s["note"] = note
			}
		}
		value := strings.ToLower(s["value"].(string))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		s["value"] = value
		out = append(out, s)
	}
	return out
}

// swapForm changes every apiVersion in the data that names the design form from to the form to, as a design file inside an answer is written in its contract's form.
func swapForm(v any, from, to string) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if k == "apiVersion" && x == from {
				t[k] = to
				continue
			}
			t[k] = swapForm(x, from, to)
		}
	case []any:
		for i, x := range t {
			t[i] = swapForm(x, from, to)
		}
	}
	return v
}

// DesignerSchema is the schema of a designer answer kind: designer's own, or for an answer mankit gives every app mankit's of the same version, as designer/app/v1 and v2 are on the envelopes of mankit/v1 and v2.
func DesignerSchema(version, kind string) string {
	own := "designer/" + version + "/" + schemaFile(kind)
	if !inFS(own) && mankitAnswer(kind) {
		return MankitSchema(version, kind)
	}
	return own
}

// mankitAnswer is whether kind is an answer mankit gives every app: mankit/v2 holds only the answers, so it alone is the list.
func mankitAnswer(kind string) bool {
	return inFS(MankitSchema("v2", kind))
}

// MankitSchema is the schema of an answer kind mankit gives every app.
func MankitSchema(version, kind string) string {
	return "mankit/" + version + "/" + schemaFile(kind)
}

// schemaFile is a kind's schema file, named in kebab case like the kind.
func schemaFile(kind string) string {
	var b strings.Builder
	for i, r := range kind {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteByte('-')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String() + schemaSuffix
}

func inFS(path string) bool {
	_, err := fs.Stat(FS, path)
	return err == nil
}

// encodeChecked writes doc as the steps write every answer, and checks it meets schema, so a step never hands on an answer its version refuses.
func encodeChecked(doc any, schema string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := Validate(schema, buf.Bytes()); err != nil {
		return nil, fmt.Errorf("the step does not give a %s answer: %w", schema, err)
	}
	return buf.Bytes(), nil
}
