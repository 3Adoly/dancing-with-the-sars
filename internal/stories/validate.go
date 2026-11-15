package stories

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dwts/backend/internal/domain"
)

// CONAECreditPrefix starts the attribution CONAE requires for SAOCOM products
// (full text: "Producto SAOCOM® - ©CONAE - <year>. Todos los derechos reservados").
const CONAECreditPrefix = "Producto SAOCOM® - ©CONAE"

// maxFileBytes is the size limit of every story file (8 MB).
const maxFileBytes = 8 << 20

var (
	idPattern = regexp.MustCompile(`^[a-z0-9-]{3,64}$`)

	// allowedExt are the extensions the media endpoint serves (Detailed E14).
	allowedExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".json": true, ".tif": true}

	layerKinds = map[string]bool{"before": true, "after": true, "change": true, "interferogram": true, "frame": true}
)

// ValidationError is one broken rule. Validate returns these as []error.
type ValidationError struct {
	// Story is the story id, or "stories[i]" when the id is empty or malformed.
	// It is empty for manifest-level problems.
	Story string
	// Field locates the problem inside the story, e.g. "layers[1].url" or
	// "explanation.driver". For manifest-level problems it is "manifest.<field>".
	Field string
	Msg   string
}

// Error reads "<story>.<field>: <msg>", e.g. "demo-placeholder.layers[1].url: file ... not found".
func (e *ValidationError) Error() string {
	switch {
	case e.Story == "":
		return e.Field + ": " + e.Msg
	case e.Field == "":
		return e.Story + ": " + e.Msg
	default:
		return e.Story + "." + e.Field + ": " + e.Msg
	}
}

// Validate checks every rule of Plan_Detailed_Level.md §10.1 against m, whose
// files are looked up under dir/<story id>/. It returns every problem found
// (each a *ValidationError naming the story and field), not just the first, and
// nil when the manifest is valid. m must come from LoadRaw, not Load: Load has
// already rewritten the file names to URLs.
func Validate(dir string, m Manifest) []error {
	v := &validator{dir: dir, seen: map[string]int{}}
	v.realDir = resolveDir(dir)
	if m.Version != ManifestVersion {
		v.errs = append(v.errs, &ValidationError{
			Field: "manifest.version",
			Msg:   fmt.Sprintf("unsupported version %d, want version %d", m.Version, ManifestVersion),
		})
	}
	for i := range m.Stories {
		v.story(i, &m.Stories[i])
	}
	return v.errs
}

type validator struct {
	dir     string
	realDir string         // dir with symlinks resolved; "" when dir does not exist
	seen    map[string]int // story id -> index of the first story using it
	errs    []error
}

func (v *validator) add(story, field, format string, args ...any) {
	v.errs = append(v.errs, &ValidationError{Story: story, Field: field, Msg: fmt.Sprintf(format, args...)})
}

// resolveDir returns the absolute, symlink-free form of dir, or "" if dir
// cannot be resolved (every file lookup then reports "not found").
func resolveDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return ""
	}
	return real
}

// story validates s, the i-th story of the manifest.
func (v *validator) story(i int, s *domain.Story) {
	name := s.ID
	idOK := idPattern.MatchString(s.ID)
	if !idOK {
		name = fmt.Sprintf("stories[%d]", i)
	}
	add := func(field, format string, args ...any) { v.add(name, field, format, args...) }

	// id
	if !idOK {
		add("id", "%q must match %s", s.ID, idPattern)
	} else if first, dup := v.seen[s.ID]; dup {
		add("id", "duplicate id, already used by stories[%d]", first)
	} else {
		v.seen[s.ID] = i
	}

	// type
	if t, ok := domain.ParseType(string(s.Type)); !ok || t != s.Type { // manifest ids must be exact (ParseType folds case)
		add("type", "%q is not a valid incident type, want one of %s", s.Type, typeNames())
	}

	// bbox
	bboxOK := true
	if msg := boundsProblem(s.BBox); msg != "" {
		add("bbox", "%s", msg)
		bboxOK = false
	}

	// dates
	date := func(field, val string) (time.Time, bool) {
		if val == "" {
			add(field, "required, format YYYY-MM-DD")
			return time.Time{}, false
		}
		t, err := time.Parse(time.DateOnly, val)
		if err != nil {
			add(field, "%q is not a valid YYYY-MM-DD date", val)
			return time.Time{}, false
		}
		return t, true
	}
	date("eventDate", s.EventDate)
	before, beforeOK := date("beforeDate", s.BeforeDate)
	after, afterOK := date("afterDate", s.AfterDate)
	if beforeOK && afterOK && !before.Before(after) {
		add("beforeDate", "%s must be earlier than afterDate %s", s.BeforeDate, s.AfterDate)
	}

	// explanation
	for _, f := range []struct{ field, val string }{
		{"explanation.driver", s.Explanation.Driver},
		{"explanation.polarizationNote", s.Explanation.PolarizationNote},
		{"explanation.howToRead", s.Explanation.HowToRead},
	} {
		if strings.TrimSpace(f.val) == "" {
			add(f.field, "must be non-empty")
		}
	}

	// files: a malformed id is never turned into a path
	file := func(field, fname string) {
		if !idOK {
			return
		}
		if msg := v.fileProblem(s.ID, fname); msg != "" {
			add(field, "%s", msg)
		}
	}
	file("thumbnail", s.Thumbnail)

	// layers
	kinds := map[string]bool{}
	for j := range s.Layers {
		l := &s.Layers[j]
		field := func(f string) string { return fmt.Sprintf("layers[%d].%s", j, f) }

		if layerKinds[l.Kind] {
			kinds[l.Kind] = true
		} else {
			add(field("kind"), "%q is not a layer kind, want before|after|change|interferogram|frame", l.Kind)
		}
		file(field("url"), l.URL)

		if msg := boundsProblem(l.Bounds); msg != "" {
			add(field("bounds"), "%s", msg)
		} else if bboxOK && !overlaps(l.Bounds, s.BBox) {
			add(field("bounds"), "%v does not overlap the story bbox %v", l.Bounds, s.BBox)
		}

		switch {
		case l.Date != "":
			date(field("date"), l.Date)
		case l.Kind == "frame":
			add(field("date"), "required for a frame layer, format YYYY-MM-DD")
		}
	}
	if !(kinds["before"] && kinds["after"]) && !kinds["change"] && !kinds["interferogram"] {
		add("layers", "need at least a before and an after layer, or a change or interferogram layer")
	}

	v.multiSensor(name, s, kinds["change"])
}

// multiSensor applies the cross-platform and CONAE-credit rules.
func (v *validator) multiSensor(name string, s *domain.Story, hasChange bool) {
	add := func(field, format string, args ...any) { v.add(name, field, format, args...) }

	// A change layer is a log-ratio of a before and an after layer, which is
	// only meaningful when both come from the change layer's own sensor. Other
	// before/after layers on a different platform are allowed: they are a
	// visual swipe only (story 5: SAOCOM before and after plus a NISAR after).
	// With no before layer or no after layer there is no source pair to
	// compare, and the change layer alone is a valid story.
	hasBefore, hasAfter := false, false
	for _, l := range s.Layers {
		hasBefore = hasBefore || l.Kind == "before"
		hasAfter = hasAfter || l.Kind == "after"
	}
	if hasBefore && hasAfter {
		for k, c := range s.Layers {
			if c.Kind != "change" {
				continue
			}
			p := effectivePlatform(s, c)
			beforeOK, afterOK := false, false
			for _, l := range s.Layers {
				if !strings.EqualFold(effectivePlatform(s, l), p) {
					continue
				}
				beforeOK = beforeOK || l.Kind == "before"
				afterOK = afterOK || l.Kind == "after"
			}
			if !beforeOK || !afterOK {
				missing := "before and after layer"
				if beforeOK {
					missing = "after layer"
				} else if afterOK {
					missing = "before layer"
				}
				add(fmt.Sprintf("layers[%d].platform", k),
					"change layer on platform %s has no %s on that platform: a change layer needs a before and an after layer from the same platform (other before/after layers are a visual swipe only)",
					quoteOrNone(p), missing)
			}
		}
	}

	// SAOCOM products may only be published with CONAE's attribution.
	saocom := -1
	for j, l := range s.Layers {
		if strings.HasPrefix(strings.ToUpper(effectivePlatform(s, l)), "SAOCOM") {
			saocom = j
			break
		}
	}
	if saocom >= 0 {
		credited := false
		for _, c := range s.Credits {
			credited = credited || strings.HasPrefix(strings.TrimSpace(c), CONAECreditPrefix)
		}
		if !credited {
			add("credits", `layers[%d] is a SAOCOM product, so credits must contain an entry starting with %q (full text: "%s - <year>. Todos los derechos reservados")`,
				saocom, CONAECreditPrefix, CONAECreditPrefix)
		}
	}
}

// effectivePlatform is the layer's own platform, or the story's when it has none.
func effectivePlatform(s *domain.Story, l domain.StoryLayer) string {
	if p := strings.TrimSpace(l.Platform); p != "" {
		return p
	}
	return strings.TrimSpace(s.Platform)
}

func quoteOrNone(p string) string {
	if p == "" {
		return "(none)"
	}
	return fmt.Sprintf("%q", p)
}

func typeNames() string {
	names := make([]string, len(domain.AllTypes))
	for i, t := range domain.AllTypes {
		names[i] = string(t)
	}
	return strings.Join(names, "|")
}

// boundsProblem returns "" when b = [west, south, east, north] is a valid
// EPSG:4326 box (-180 <= w < e <= 180, -90 <= s < n <= 90; NaN fails every
// comparison, so it is rejected too).
func boundsProblem(b [4]float64) string {
	w, s, e, n := b[0], b[1], b[2], b[3]
	if !(w >= -180 && e <= 180 && w < e) {
		return fmt.Sprintf("%v: longitudes need -180 <= west < east <= 180", b)
	}
	if !(s >= -90 && n <= 90 && s < n) {
		return fmt.Sprintf("%v: latitudes need -90 <= south < north <= 90", b)
	}
	if math.IsNaN(w + s + e + n) {
		return fmt.Sprintf("%v: not a number", b)
	}
	return ""
}

// overlaps reports whether two [w, s, e, n] boxes share some area.
func overlaps(a, b [4]float64) bool {
	return a[0] < b[2] && b[0] < a[2] && a[1] < b[3] && b[1] < a[3]
}

// fileProblem checks the story file fname of story id and returns "" if it is
// fine: a plain file name (no path, no "..", never leaving the story dir), an
// allowed extension, an existing regular file inside the stories dir (after
// resolving symlinks) and at most 8 MB.
func (v *validator) fileProblem(id, fname string) string {
	switch {
	case fname == "":
		return "required, a file name inside the story directory"
	case strings.ContainsAny(fname, "/\\\x00") || strings.Contains(fname, "..") || fname == "." || filepath.Base(fname) != fname:
		return fmt.Sprintf(`file name %q must not contain "/", "\" or ".."`, fname)
	}
	if ext := filepath.Ext(fname); !allowedExt[ext] {
		return fmt.Sprintf("file %q has extension %q, want one of .png, .jpg, .jpeg, .webp, .json, .tif", fname, ext)
	}
	rel := filepath.Join(id, fname)
	real, err := filepath.EvalSymlinks(filepath.Join(v.dir, id, fname))
	if errors.Is(err, fs.ErrNotExist) || (err == nil && v.realDir == "") {
		return fmt.Sprintf("file %q not found in the stories directory", rel)
	}
	if err != nil {
		return fmt.Sprintf("file %q: %v", rel, err)
	}
	if abs, err := filepath.Abs(real); err != nil || !within(v.realDir, abs) {
		return fmt.Sprintf("file %q resolves outside the stories directory", rel)
	}
	info, err := os.Stat(real)
	if err != nil {
		return fmt.Sprintf("file %q: %v", rel, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Sprintf("%q is not a regular file", rel)
	}
	if info.Size() > maxFileBytes {
		return fmt.Sprintf("file %q is %d bytes, over the 8 MB limit", rel, info.Size())
	}
	return ""
}

// within reports whether path is root or lies below it. Both must be absolute
// and symlink-free.
func within(root, path string) bool {
	r, err := filepath.Rel(root, path)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}
