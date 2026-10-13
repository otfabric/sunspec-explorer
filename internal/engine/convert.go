// SPDX-License-Identifier: MIT

package engine

import (
	"math"
	"strings"

	"github.com/otfabric/go-modbus"
	sunspec "github.com/otfabric/go-sunspec"
	"github.com/otfabric/go-sunspec/registry"

	"github.com/otfabric/sunspec-explorer/internal/model"
)

func regTypeName(t modbus.RegType) string {
	if t == modbus.InputRegister {
		return model.RegTypeInput
	}
	return model.RegTypeHolding
}

func attemptsOf(in []sunspec.ProbeAttempt) []model.ProbeAttempt {
	out := make([]model.ProbeAttempt, len(in))
	for i, a := range in {
		out[i] = model.ProbeAttempt{
			BaseAddress: a.BaseAddress,
			RegType:     regTypeName(a.RegType),
			Matched:     a.Matched,
			Error:       a.ErrorString,
		}
		if out[i].Error == "" && a.Error != nil {
			out[i].Error = a.Error.Error()
		}
	}
	return out
}

func modelInfos(d *sunspec.DiscoveryResult) []model.ModelInfo {
	out := make([]model.ModelInfo, len(d.Models))
	for i, inst := range d.Models {
		out[i] = model.ModelInfo{
			Index:             i,
			ID:                inst.Header.ID,
			Name:              inst.Name,
			StartAddress:      inst.Header.StartAddress,
			Length:            inst.Header.Length,
			SchemaKnown:       inst.SchemaKnown,
			DecodingSupported: inst.DecodingSupported,
			StaticOnly:        inst.Schema != nil && staticOnly(inst.Schema.Group),
		}
	}
	return out
}

// staticOnly reports whether the group, with the groups nested in it, holds
// nothing that changes by itself: every point is static, padding, or a
// writable setting.
//
// No SunSpec model consists of static points alone: Common, the model this is
// about, has the writable device address DA. A setting changes only when
// somebody writes it, so it does not make a model worth polling.
func staticOnly(g *registry.GroupMeta) bool {
	if g == nil {
		return false
	}
	for i := range g.Points {
		if p := &g.Points[i]; !p.Static && p.Type != "pad" && p.Access != "RW" {
			return false
		}
	}
	for _, c := range g.Groups {
		if !staticOnly(c) {
			return false
		}
	}
	return true
}

// groupView converts a decoded group and joins the metadata of its schema.
// meta may be nil: the points then carry no metadata.
func groupView(g *sunspec.DecodedGroup, meta *registry.GroupMeta, start uint16, full bool) *model.GroupView {
	if g == nil {
		return nil
	}
	out := &model.GroupView{
		Name:   g.Name,
		Index:  g.Index,
		Points: make([]model.PointView, len(g.Points)),
		Groups: make([]model.GroupView, 0, len(g.Groups)),
	}
	if full && meta != nil {
		out.Label = meta.Label
	}
	for i := range g.Points {
		out.Points[i] = pointView(&g.Points[i], meta.Point(g.Points[i].Name), start, full)
	}
	for _, c := range g.Groups {
		out.Groups = append(out.Groups, *groupView(c, meta.Group(c.Name), start, full))
	}
	return out
}

func pointView(p *sunspec.DecodedPoint, meta *registry.PointMeta, start uint16, full bool) model.PointView {
	v := model.PointView{
		Name:        p.Name,
		Type:        p.Type,
		Units:       p.Units,
		Implemented: p.Implemented,
		Raw:         jsonSafe(p.RawValue),
		SFName:      p.SFName,
		SFRaw:       p.SFRawValue,
		Symbols:     p.Symbols,
		Address:     int(start) + p.RegisterOffset,
		Offset:      p.RegisterOffset,
		Size:        p.RegisterCount,
	}
	// Padding is filler, not a value: go-sunspec decodes it like a number.
	if p.Type == "pad" {
		v.Implemented = false
	}
	if p.ScaledValue != nil && isFinite(*p.ScaledValue) {
		s := *p.ScaledValue
		v.Scaled = &s
	}
	if full && meta != nil {
		v.Label = meta.Label
		v.Description = meta.Desc
		v.Access = meta.Access
		mandatory, static := meta.Mandatory, meta.Static
		v.Mandatory = &mandatory
		v.Static = &static
	}
	return v
}

// jsonSafe replaces what JSON cannot carry: a float that is NaN or infinite
// (what a float point reported as not implemented decodes to) becomes nil.
func jsonSafe(v any) any {
	switch f := v.(type) {
	case float32:
		if !isFinite(float64(f)) {
			return nil
		}
	case float64:
		if !isFinite(f) {
			return nil
		}
	}
	return v
}

func isFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// identityOf extracts the device identification from a decoded Common model.
func identityOf(dm *sunspec.DecodedModel) *model.DeviceIdentity {
	if dm == nil || dm.Group == nil {
		return nil
	}
	str := func(name string) string {
		p := dm.Group.Point(name)
		if p == nil || !p.Implemented {
			return ""
		}
		s, _ := p.RawValue.(string)
		return strings.TrimSpace(s)
	}
	return &model.DeviceIdentity{
		Manufacturer: str("Mn"),
		Model:        str("Md"),
		Options:      str("Opt"),
		Version:      str("Vr"),
		Serial:       str("SN"),
	}
}
