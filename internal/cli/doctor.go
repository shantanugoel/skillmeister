package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shantanugoel/skillmeister/internal/doctor"
	"github.com/shantanugoel/skillmeister/internal/harness"
	"github.com/shantanugoel/skillmeister/internal/security"
)

func cmdDoctor(g *globals, args []string) error {
	fs := newFlagSet(g, "doctor")
	asJSON := fs.Bool("json", false, "print one JSON object (best effort, not versioned)")
	sec := fs.Bool("security", false, "also run the security heuristics; exit 2 on a high-severity hit")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErr("doctor: unexpected argument %q", fs.Arg(0))
	}
	lib, err := g.openLibrary()
	if err != nil {
		return err
	}
	defer lib.Close()
	rep, err := doctor.Run(lib)
	if err != nil {
		return ioErr(err)
	}
	rep.Findings = append(rep.Findings, harness.Check(g.env.Getenv("HOME"), lib)...)
	if *sec {
		hits, err := security.Scan(lib)
		if err != nil {
			return ioErr(err)
		}
		for _, h := range hits {
			sev := doctor.Error
			if h.Severity != security.High {
				sev = doctor.Warn
			}
			rep.Findings = append(rep.Findings, doctor.Finding{
				Severity: sev, Code: "sec-" + h.Rule, Pack: h.Pack, Subject: h.Skill, Message: h.String(),
			})
		}
	}
	if rep.Findings == nil {
		rep.Findings = []doctor.Finding{}
	}
	if rep.Packs == nil {
		rep.Packs = []doctor.PackStatus{}
	}
	if *asJSON {
		enc := json.NewEncoder(g.env.Stdout)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(struct {
			*doctor.Report
			OK bool `json:"ok"`
		}{rep, !rep.Failed()}); err != nil {
			return ioErr(err)
		}
	} else {
		g.printReport(rep)
	}
	if rep.Failed() {
		n := 0
		for _, f := range rep.Findings {
			if f.Severity == doctor.Error {
				n++
			}
		}
		return &exitError{ExitDrift, fmt.Errorf("doctor: %d errors", n)}
	}
	return nil
}

func (g *globals) printReport(rep *doctor.Report) {
	g.printf("skillmeister %s  library %s\n", rep.Version, rep.Library)
	g.printf("packs:\n")
	for _, p := range rep.Packs {
		state := "enabled"
		if !p.Enabled {
			state = "disabled"
		}
		verified := ""
		if p.Verified {
			verified = "  verified"
		}
		trust := string(p.Declared)
		if p.Effective != trust {
			trust += " -> " + strings.ToUpper(p.Effective)
		}
		g.printf("  %-16s %-5s %-9s %-22s %3d skills%s\n", p.Name, p.Fetch, state, trust, p.Skills, verified)
	}
	if len(rep.Findings) == 0 {
		g.printf("no findings\n")
		return
	}
	g.printf("findings:\n")
	for _, f := range rep.Findings {
		g.printFinding(f)
	}
}

func (g *globals) printFinding(f doctor.Finding) {
	subject := f.Subject
	if subject == "" {
		subject = f.Pack
	}
	g.printf("  %-5s %-18s %-28s %s\n", f.Severity, f.Code, subject, escape(f.Message))
}
