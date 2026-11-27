package contracts_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.naywee.com/contracts/v2"
)

// steps is every upgrade step by its folder in testdata/upgrade, with the schema of the version it reads and the version it makes, each by the answer's kind.
var steps = map[string]struct {
	step     func([]byte) ([]byte, error)
	from, to func(kind string) string
}{
	"envelope/v1-v2": {contracts.UpgradeEnvelope, fixed("envelope/v1/envelope.schema.json"), fixed("envelope/v2/envelope.schema.json")},
	"designer/v1-v2": {contracts.UpgradeDesigner, designerSchema("v1"), designerSchema("v2")},
	"mankit/v1-v2":   {contracts.UpgradeMankit, mankitSchema("v1"), mankitSchema("v2")},
	"mankit/v2-v3":   {contracts.UpgradeLicenseShow, mankitSchema("v2"), mankitSchema("v3")},
}

func fixed(schema string) func(string) string { return func(string) string { return schema } }

func designerSchema(version string) func(string) string {
	return func(kind string) string { return contracts.DesignerSchema(version, kind) }
}

func mankitSchema(version string) func(string) string {
	return func(kind string) string { return contracts.MankitSchema(version, kind) }
}

// stepOf is the step of a file in testdata/upgrade/<dir>/<from>-<to>/, or a failure naming the folder with no step.
func stepOf(t *testing.T, path string) (func([]byte) ([]byte, error), func(string) string, func(string) string) {
	t.Helper()
	folder := filepath.ToSlash(strings.TrimPrefix(filepath.Dir(path), filepath.Join("testdata", "upgrade")+string(filepath.Separator)))
	s, ok := steps[folder]
	if !ok {
		t.Fatalf("no upgrade step for %s", folder)
	}
	return s.step, s.from, s.to
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func decode(t *testing.T, b []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, b)
	}
	return v
}

// versionsOf is the two versions of a file in testdata/upgrade/<dir>/<from>-<to>/, by its folder's name.
func versionsOf(path string) (from, to string) {
	from, to, _ = strings.Cut(filepath.Base(filepath.Dir(path)), "-")
	return from, to
}

// Each pair in testdata/upgrade/<dir>/<from>-<to>/ is an answer of the older version, <case>.<from>.json, and the answer its step makes of it, <case>.<to>.json, each meeting its own version (CT-RULE-3).
func TestUpgradeStepsMatchTheirSamples(t *testing.T) {
	files, _ := filepath.Glob("testdata/upgrade/*/*/*.json")
	var pairs []string
	for _, path := range files {
		if from, _ := versionsOf(path); strings.HasSuffix(path, "."+from+".json") {
			pairs = append(pairs, path)
		}
	}
	if len(pairs) == 0 {
		t.Fatal("no upgrade pairs found")
	}
	for _, path := range pairs {
		t.Run(path, func(t *testing.T) {
			step, fromSchema, toSchema := stepOf(t, path)
			from, to := versionsOf(path)
			older, want := readFile(t, path), readFile(t, strings.TrimSuffix(path, "."+from+".json")+"."+to+".json")
			kind := kindOf(t, older)
			if err := contracts.Validate(fromSchema(kind), older); err != nil {
				t.Fatalf("the sample read does not meet %s: %v", fromSchema(kind), err)
			}
			if err := contracts.Validate(toSchema(kind), want); err != nil {
				t.Fatalf("the sample made does not meet %s: %v", toSchema(kind), err)
			}
			got, err := step(older)
			if err != nil {
				t.Fatal(err)
			}
			// A detail is read by people, so <, > and & stay as they are.
			if strings.Contains(string(got), `\u003c`) || strings.Contains(string(got), `\u0026`) {
				t.Fatalf("the step escaped text meant for people:\n%s", got)
			}
			if !reflect.DeepEqual(decode(t, got), decode(t, want)) {
				t.Fatalf("the step gave\n%s\nwant\n%s", got, want)
			}
		})
	}
}

func kindOf(t *testing.T, b []byte) string {
	t.Helper()
	var head struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		t.Fatal(err)
	}
	return head.Kind
}

// Every file in testdata/upgrade is part of one pair, or refused: a sample with no partner is a pair cut in half.
func TestEveryUpgradeSampleHasItsPartner(t *testing.T) {
	files, err := filepath.Glob("testdata/upgrade/*/*/*.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		from, to := versionsOf(path)
		var partner string
		switch {
		case strings.HasSuffix(path, ".refused.json"):
			continue
		case strings.HasSuffix(path, "."+from+".json"):
			partner = strings.TrimSuffix(path, "."+from+".json") + "." + to + ".json"
		case strings.HasSuffix(path, "."+to+".json"):
			partner = strings.TrimSuffix(path, "."+to+".json") + "." + from + ".json"
		default:
			t.Errorf("%s is neither a .%s.json, a .%s.json nor a .refused.json", path, from, to)
			continue
		}
		if _, err := os.Stat(partner); err != nil {
			t.Errorf("%s has no partner %s", path, partner)
		}
	}
}

// What a step must refuse: an answer that is not of the version it reads.
func TestUpgradeStepsRefuseWhatIsNotTheirs(t *testing.T) {
	refused, err := filepath.Glob("testdata/upgrade/*/*/*.refused.json")
	if err != nil || len(refused) == 0 {
		t.Fatalf("no refused upgrade samples found: %v", err)
	}
	for _, path := range refused {
		t.Run(path, func(t *testing.T) {
			step, _, _ := stepOf(t, path)
			if out, err := step(readFile(t, path)); err == nil {
				t.Fatalf("the step took it and gave\n%s", out)
			}
		})
	}
}
