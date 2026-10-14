// SPDX-License-Identifier: MIT

package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	sunspec "github.com/otfabric/go-sunspec"
	"github.com/otfabric/go-sunspec/devicefile"

	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

// csvHeader is the long format of every CSV the engine writes: one row per
// point per read. seq is the sample number in a recording and empty in the
// export of a single read.
var csvHeader = []string{
	"captured_at", "seq", "model_index", "model_id", "group_path", "point", "value", "scaled", "unit", "implemented", "status",
}

// ExportSession implements service.Service.
func (e *Engine) ExportSession(ctx context.Context, id string, format string) (model.ExportFile, error) {
	s, err := e.session(id)
	if err != nil {
		return model.ExportFile{}, err
	}
	switch format {
	case model.ExportCSV, model.ExportDeviceFile:
	default:
		return model.ExportFile{}, service.Errorf(service.CodeInvalidArgument, "export format %q: use %q or %q",
			format, model.ExportCSV, model.ExportDeviceFile)
	}
	if err := s.acquire(ctx); err != nil {
		return model.ExportFile{}, err
	}
	defer s.release()

	stamp := time.Now().UTC().Format("20060102T150405Z")
	if format == model.ExportCSV {
		indexes, _ := s.resolveIndexes(nil)
		res := s.read(ctx, indexes, false, false)
		if err := ctx.Err(); err != nil {
			return model.ExportFile{}, err
		}
		var buf bytes.Buffer
		w := csv.NewWriter(&buf)
		_ = w.Write(csvHeader)
		writeCSVRows(w, res.start.UTC(), "", res.models)
		w.Flush()
		return model.ExportFile{Name: "sunspec-points-" + stamp + ".csv", Mime: "text/csv", Text: buf.String()}, nil
	}

	text, err := s.deviceFile(ctx)
	if err != nil {
		return model.ExportFile{}, err
	}
	return model.ExportFile{Name: "sunspec-device-" + stamp + ".json", Mime: "application/json", Text: text}, nil
}

// writeCSVRows writes the rows of one read: a row per point, and for a model
// that was not decoded a row per data register.
func writeCSVRows(w *csv.Writer, at time.Time, seq string, models []model.ModelResult) {
	captured := at.Format(time.RFC3339Nano)
	for i := range models {
		m := &models[i]
		prefix := []string{captured, seq, strconv.Itoa(m.Index), strconv.Itoa(int(m.ID))}
		row := func(path, point, value, scaled, unit, implemented string) {
			_ = w.Write(append(prefix[:4:4], path, point, value, scaled, unit, implemented, m.Status))
		}
		switch {
		case m.Group != nil:
			writeCSVGroup(row, "", m.Group)
		case len(m.RawRegisters) > 0:
			for n, reg := range m.RawRegisters {
				row("", "register["+strconv.Itoa(n)+"]", strconv.Itoa(int(reg)), "", "", "")
			}
		default:
			// A model that failed: one row that says so.
			row("", "", "", "", "", "")
		}
	}
}

func writeCSVGroup(row func(path, point, value, scaled, unit, implemented string), path string, g *model.GroupView) {
	for i := range g.Points {
		p := &g.Points[i]
		value, scaled := "", ""
		if p.Implemented {
			value = csvValue(p.Raw)
			if p.Scaled != nil {
				scaled = strconv.FormatFloat(*p.Scaled, 'g', -1, 64)
			}
		}
		row(path, p.Name, value, scaled, p.Units, strconv.FormatBool(p.Implemented))
	}
	for i := range g.Groups {
		c := &g.Groups[i]
		sub := fmt.Sprintf("%s[%d]", c.Name, c.Index)
		if path != "" {
			sub = path + "/" + sub
		}
		writeCSVGroup(row, sub, c)
	}
}

// csvValue renders a raw point value: numbers and strings as they are,
// registers of an undecoded type separated by spaces. A value that came
// through JSON (a recording) is a float64 or a list.
func csvValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'g', -1, 32)
	case []uint16:
		parts := make([]string, len(x))
		for i, r := range x {
			parts[i] = strconv.Itoa(int(r))
		}
		return strings.Join(parts, " ")
	case []any:
		parts := make([]string, len(x))
		for i, r := range x {
			parts[i] = csvValue(r)
		}
		return strings.Join(parts, " ")
	}
	return fmt.Sprint(v)
}

// OpenRecordingCSV implements service.Service. The conversion runs as the
// result is read, so a recording of any size takes little memory.
func (e *Engine) OpenRecordingCSV(ctx context.Context, recID string) (io.ReadCloser, model.RecordingInfo, error) {
	if e.recs == nil {
		return nil, model.RecordingInfo{}, errNoDataDir()
	}
	src, info, err := e.recs.open(recID, 0)
	if err != nil {
		return nil, model.RecordingInfo{}, err
	}
	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = src.Close() }()
		w := csv.NewWriter(pw)
		_ = w.Write(csvHeader)
		sc := bufio.NewScanner(src)
		sc.Buffer(make([]byte, 64<<10), 16<<20)
		for sc.Scan() && ctx.Err() == nil {
			var line model.RecordingSample
			// Header, event and footer lines, and a truncated last line,
			// are not samples.
			if json.Unmarshal(sc.Bytes(), &line) != nil || line.Type != model.LineSample {
				continue
			}
			writeCSVRows(w, line.CapturedAt, strconv.FormatUint(line.Seq, 10), line.Models)
			w.Flush()
			if w.Error() != nil {
				break
			}
		}
		w.Flush()
		err := sc.Err()
		if err == nil {
			err = ctx.Err()
		}
		_ = pw.CloseWithError(err)
	}()
	return pr, info, nil
}

// deviceFile reads every model and describes the device as a device file
// for "sunspecctl serve", in go-sunspec's devicefile format: a model that
// decodes cleanly by its point values, any other one by its registers. The
// caller holds the I/O token. Unlike a snapshot, a device file is all or
// nothing: with a model missing it would describe another device. (The read
// loop is the engine's own, not devicefile.Capture, so that it paces its
// requests and reports failures as service errors.)
func (s *session) deviceFile(ctx context.Context) (string, error) {
	file := devicefile.New(s.dev.UnitID, s.dev.Discovery.BaseAddress)
	for n, inst := range s.dev.Discovery.Models {
		if n > 0 && !s.pace(ctx) {
			return "", ctx.Err()
		}
		dm, err := s.dev.ReadModel(ctx, inst)
		// A model that does not fit its schema is still captured, from its
		// registers. Anything else means the registers are not all there.
		if err != nil && !errors.Is(err, sunspec.ErrDecode) {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", service.Wrap(service.CodeConnectFailed, err, "model %d at position %d could not be read", inst.Header.ID, n)
		}
		file.Models = append(file.Models, devicefile.FromDecoded(dm, false))
	}
	b, err := file.Marshal()
	if err != nil {
		return "", service.Wrap(service.CodeInternal, err, "device file")
	}
	return string(b), nil
}
