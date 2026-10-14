// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/otfabric/sunspec-explorer/internal/model"
)

// snapshotSchema compiles the published schema of the snapshot export.
func snapshotSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "docs", "schema", "snapshot-1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	schema, err := c.Compile(path)
	if err != nil {
		t.Fatalf("the schema does not compile: %v", err)
	}
	return schema
}

func validate(t *testing.T, schema *jsonschema.Schema, doc []byte) error {
	t.Helper()
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	return schema.Validate(v)
}

// A8: what the Explorer exports validates against the schema it publishes,
// in every shape a snapshot takes.
func TestSnapshotMatchesPublishedSchema(t *testing.T) {
	schema := snapshotSchema(t)
	d := newSim(t)
	e := newEngine(t, time.Second)
	info := mustOpen(t, e, d)

	export := func(req model.SnapshotRequest) []byte {
		t.Helper()
		snap, err := e.Snapshot(ctx, info.ID, req)
		if err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
		b, err := json.Marshal(snap)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	full := export(model.SnapshotRequest{})
	for name, doc := range map[string][]byte{
		"full, every model":    full,
		"with raw registers":   export(model.SnapshotRequest{IncludeRaw: true}),
		"compact, a selection": export(model.SnapshotRequest{Metadata: model.MetadataCompact, ModelIndexes: []int{idxVoltVar, idxVendor}}),
	} {
		if err := validate(t, schema, doc); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A snapshot with a failed model, which has an error and no group.
	inv := info.Models[idxInverter]
	d.setFilter(failRange(inv.StartAddress, inv.StartAddress+1, modbus.ErrServerDeviceFailure))
	failed := export(model.SnapshotRequest{})
	if !bytes.Contains(failed, []byte(`"status":"error"`)) {
		t.Fatal("no failed model in the snapshot")
	}
	if err := validate(t, schema, failed); err != nil {
		t.Errorf("with a failed model: %v", err)
	}

	// The schema is not vacuous: it refuses what is not a snapshot.
	for name, edit := range map[string]func(string) string{
		"another schema id":  func(s string) string { return strings.Replace(s, "snapshot/1", "snapshot/2", 1) },
		"an unknown field":   func(s string) string { return strings.Replace(s, `"session_id"`, `"password":"x","session_id"`, 1) },
		"a missing field":    func(s string) string { return strings.Replace(s, `"captured_at"`, `"captured"`, 1) },
		"an unknown status":  func(s string) string { return strings.Replace(s, `"status":"ok"`, `"status":"fine"`, 1) },
		"a text address":     func(s string) string { return strings.Replace(s, `"base_address":40000`, `"base_address":"40000"`, 1) },
		"a point field typo": func(s string) string { return strings.Replace(s, `"implemented":`, `"implementd":`, 1) },
	} {
		if err := validate(t, schema, []byte(edit(string(full)))); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
