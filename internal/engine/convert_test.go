// SPDX-License-Identifier: MIT

package engine

import (
	"errors"
	"math"
	"testing"

	"github.com/otfabric/go-modbus"
	sunspec "github.com/otfabric/go-sunspec"
	"github.com/otfabric/go-sunspec/registry"

	"github.com/otfabric/sunspec-explorer/internal/model"
)

func TestStaticOnly(t *testing.T) {
	// Common has one point that is not static, the writable device address.
	if !staticOnly(registry.ByID(1).Group) {
		t.Error("Common is not static-only")
	}
	for _, id := range []uint16{103, 160, 705} {
		if staticOnly(registry.ByID(id).Group) {
			t.Errorf("model %d is static-only", id)
		}
	}
	if staticOnly(nil) {
		t.Error("no schema is static-only")
	}
	nested := &registry.GroupMeta{
		Points: []registry.PointMeta{{Name: "ID", Static: true}, {Name: "Pad", Type: "pad"}},
		Groups: []*registry.GroupMeta{{Points: []registry.PointMeta{{Name: "W", Access: "R"}}}},
	}
	if staticOnly(nested) {
		t.Error("a measurement in a nested group was missed")
	}
}

func TestPointViewValues(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	for _, v := range []any{float32(nan), nan, float32(inf), inf} {
		if jsonSafe(v) != nil {
			t.Errorf("jsonSafe(%v) is not nil", v)
		}
	}
	if jsonSafe(float32(1.5)) != float32(1.5) || jsonSafe(2.5) != 2.5 || jsonSafe("x") != "x" {
		t.Error("jsonSafe changed a finite value")
	}

	// A float point the device reports as not implemented: no NaN leaks out.
	p := sunspec.DecodedPoint{Name: "W", Type: "float32", RawValue: float32(nan), ScaledValue: &nan, RegisterOffset: 4, RegisterCount: 2}
	v := pointView(&p, nil, 40002, true)
	if v.Raw != nil || v.Scaled != nil || v.Address != 40006 || v.Size != 2 || v.Mandatory != nil {
		t.Errorf("pointView = %+v", v)
	}
	if groupView(nil, nil, 0, true) != nil {
		t.Error("groupView(nil)")
	}
}

func TestIdentityOf(t *testing.T) {
	if identityOf(nil) != nil || identityOf(&sunspec.DecodedModel{}) != nil {
		t.Error("identity of an undecoded model")
	}
	dm := &sunspec.DecodedModel{Group: &sunspec.DecodedGroup{Points: []sunspec.DecodedPoint{
		{Name: "Mn", RawValue: " Acme ", Implemented: true},
		{Name: "SN", RawValue: "", Implemented: false},
	}}}
	id := identityOf(dm)
	if id.Manufacturer != "Acme" || id.Serial != "" || id.Model != "" {
		t.Errorf("identity = %+v", id)
	}
}

func TestSmallHelpers(t *testing.T) {
	if regTypeName(modbus.InputRegister) != model.RegTypeInput || regTypeName(modbus.HoldingRegister) != model.RegTypeHolding {
		t.Error("regTypeName")
	}
	boom := errors.New("boom")
	got := attemptsOf([]sunspec.ProbeAttempt{{BaseAddress: 1, Error: boom}, {BaseAddress: 2, Matched: true}})
	if got[0].Error != "boom" || got[1].Error != "" || !got[1].Matched {
		t.Errorf("attemptsOf = %+v", got)
	}
	if errText(nil) != "" || schemaGroup(nil) != nil || dataRegisters([]uint16{1}) != nil {
		t.Error("nil handling")
	}
	if deviceAnswered(boom) || !deviceAnswered(&sunspec.DecodeError{}) || !deviceAnswered(modbus.ErrServerDeviceBusy) {
		t.Error("deviceAnswered")
	}
}
