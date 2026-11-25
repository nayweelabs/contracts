package contracts_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ElderlyGate/contracts"
)

func TestValidateTakesASample(t *testing.T) {
	samples, err := filepath.Glob("*/*/samples/*.json")
	if err != nil || len(samples) == 0 {
		t.Fatalf("no samples: %v", err)
	}
	for _, sample := range samples {
		name := strings.TrimSuffix(filepath.Base(sample), ".json")
		schema := filepath.ToSlash(filepath.Join(filepath.Dir(filepath.Dir(sample)), name+".schema.json"))
		doc, err := os.ReadFile(sample)
		if err != nil {
			t.Fatal(err)
		}
		if err := contracts.Validate(schema, doc); err != nil {
			t.Errorf("%s: %v", sample, err)
		}
	}
}

func TestValidateIsSafeFromManyGoroutines(t *testing.T) {
	doc, err := os.ReadFile("envelope/v2/samples/envelope.json")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 8 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			for range 50 {
				if err := contracts.Validate("envelope/v2/envelope.schema.json", doc); err != nil {
					t.Error(err)
				}
				if err := contracts.Validate("version/v1/version.schema.json", doc); err == nil {
					t.Error("a greeting was taken as a version")
				}
			}
		})
	}
}

func TestValidateRefusesABrokenDocument(t *testing.T) {
	doc, err := os.ReadFile("testdata/bad/envelope/v2/envelope/no-source.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := contracts.Validate("envelope/v2/envelope.schema.json", doc); err == nil {
		t.Fatal("a document without source was taken")
	}
}

func TestValidateFollowsAReferenceToAnotherSchema(t *testing.T) {
	doc := []byte(`{"apiVersion":"elderlygate/example/app/v2","kind":"Greeting","source":{"name":"example","app":"example","version":"2.0.0","contract":"v2"},"data":null,` +
		`"errors":[{"apiVersion":"elderlygate/contracts/problem/v1","kind":"Problem","type":"unsupported_contract"}]}`)
	if err := contracts.Validate("envelope/v2/envelope.schema.json", doc); err == nil {
		t.Fatal("a Problem without its detail was taken")
	}
}

func TestValidateNamesAMissingSchema(t *testing.T) {
	err := contracts.Validate("nothing/v1/nothing.schema.json", []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "nothing/v1/nothing.schema.json") {
		t.Fatalf("got %v, want an error naming the schema", err)
	}
}

func TestValidateRefusesADocumentThatIsNotJSON(t *testing.T) {
	if err := contracts.Validate("envelope/v2/envelope.schema.json", []byte("not json")); err == nil {
		t.Fatal("a document that is not JSON was taken")
	}
}

func TestAServerRecordTakesNewSpecFieldsButNoNewHeader(t *testing.T) {
	sample, err := os.ReadFile("mankit/v2/samples/server.json")
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		APIVersion string         `json:"apiVersion"`
		Kind       string         `json:"kind"`
		Metadata   map[string]any `json:"metadata"`
		Spec       map[string]any `json:"spec"`
	}
	if err := json.Unmarshal(sample, &rec); err != nil {
		t.Fatal(err)
	}
	rec.Spec["queue"] = "a field a newer app adds"
	newer, _ := json.Marshal(rec)
	if err := contracts.Validate("mankit/v2/server.schema.json", newer); err != nil {
		t.Fatalf("a record from a newer app was refused: %v", err)
	}
	delete(rec.Spec, "queue")
	rec.Metadata["host"] = "a renamed header"
	changed, _ := json.Marshal(rec)
	if err := contracts.Validate("mankit/v2/server.schema.json", changed); err == nil {
		t.Fatal("a record with a new header field was taken")
	}
}

func TestATicketTakesNewSpecFieldsButNoNewHeader(t *testing.T) {
	sample, err := os.ReadFile("mankit/v2/samples/queue-ticket.json")
	if err != nil {
		t.Fatal(err)
	}
	var ticket struct {
		APIVersion string         `json:"apiVersion"`
		Kind       string         `json:"kind"`
		Metadata   map[string]any `json:"metadata"`
		Spec       map[string]any `json:"spec"`
	}
	if err := json.Unmarshal(sample, &ticket); err != nil {
		t.Fatal(err)
	}
	ticket.Spec["priority"] = "a field a newer app adds"
	newer, err := json.Marshal(ticket)
	if err != nil {
		t.Fatal(err)
	}
	if err := contracts.Validate("mankit/v2/queue-ticket.schema.json", newer); err != nil {
		t.Fatalf("a ticket from a newer app was refused: %v", err)
	}
	delete(ticket.Spec, "priority")
	ticket.Metadata["machine"] = ticket.Metadata["host"]
	delete(ticket.Metadata, "host")
	renamed, err := json.Marshal(ticket)
	if err != nil {
		t.Fatal(err)
	}
	if err := contracts.Validate("mankit/v2/queue-ticket.schema.json", renamed); err == nil {
		t.Fatal("a ticket with a renamed header field was taken")
	}
}

// licenseFileProblem checks what the license file schema cannot say, as the payload sits inside the signed base64: the payload meets its schema and names the file's id, only a release-trial's file has spec.started, and its day is never after the payload's until.
func licenseFileProblem(raw []byte) error {
	var file struct {
		Metadata struct {
			ID string `json:"id"`
		} `json:"metadata"`
		Spec struct {
			Payload []byte `json:"payload"`
			Started *struct {
				Day string `json:"day"`
			} `json:"started"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return err
	}
	if err := contracts.Validate("mankit/v2/license-payload.schema.json", file.Spec.Payload); err != nil {
		return fmt.Errorf("its payload: %w", err)
	}
	var payload struct {
		ID    string `json:"id"`
		Kind  string `json:"kind"`
		Until string `json:"until"`
	}
	if err := json.Unmarshal(file.Spec.Payload, &payload); err != nil {
		return err
	}
	if payload.ID != file.Metadata.ID {
		return fmt.Errorf("metadata.id %q is not the payload's id %q", file.Metadata.ID, payload.ID)
	}
	if file.Spec.Started == nil {
		return nil
	}
	if payload.Kind != "release-trial" {
		return fmt.Errorf("spec.started in the file of a %s license, not a release-trial", payload.Kind)
	}
	day, err := time.Parse(time.DateOnly, file.Spec.Started.Day)
	if err != nil {
		return err
	}
	until, err := time.Parse(time.DateOnly, payload.Until)
	if err != nil {
		return err
	}
	if day.After(until) {
		return fmt.Errorf("spec.started.day %s is after the payload's until %s", file.Spec.Started.Day, payload.Until)
	}
	return nil
}

func TestLicenseFilePayloads(t *testing.T) {
	files, _ := filepath.Glob("testdata/good/mankit/v2/license-file/*.json")
	files = append(files, "mankit/v2/samples/license-file.json")
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := licenseFileProblem(raw); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}

// Each file in testdata/bad/mankit/v2/license-file/payload/ meets the file schema but breaks one rule only its payload decides.
func TestLicenseFilePayloadRulesAreCaught(t *testing.T) {
	files, _ := filepath.Glob("testdata/bad/mankit/v2/license-file/payload/*.json")
	if len(files) == 0 {
		t.Fatal("no broken license files found")
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := contracts.Validate("mankit/v2/license-file.schema.json", raw); err != nil {
			t.Errorf("%s: breaks the file schema, not only a payload rule: %v", path, err)
		}
		if licenseFileProblem(raw) == nil {
			t.Errorf("%s: a broken license file was accepted", path)
		}
	}
}
