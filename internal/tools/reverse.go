package tools

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/allones/allones/internal/secure"
)

func registerReverse(r *Registry) {
	r.Add(&Tool{Name: "binary_inspect", Category: "reverse",
		Description: "Inspect a shipped binary or artifact without running it: detect the format, hashes, and for ELF/PE/Mach-O the architecture, imports/exports, sections, linked libraries, RELRO/NX/PIE/canary hardening and embedded version strings. Evidence-first: reports what is present, and absence as observed absence.",
		Schema:      Obj(map[string]any{"path": Str("Binary file in the workspace."), "detail": Enum("Depth.", "summary", "full")}, "path"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct{ Path, Detail string }
			Bind(raw, &a)
			p, err := env.Resolve(a.Path)
			if err != nil {
				return Errorf("%v", err)
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return Errorf("%v", err)
			}
			return Text(InspectBinary(a.Path, b, a.Detail == "full"))
		}})

	r.Add(&Tool{Name: "strings_extract", Category: "reverse",
		Description: "Extract printable ASCII/UTF-16 strings from a binary file, optionally filtered by a regular expression. Useful for finding URLs, keys, paths, version strings and endpoints inside shipped artifacts.",
		Schema:      Obj(map[string]any{"path": Str("File."), "min": Int("Minimum length, default 5."), "filter": Str("Keep strings matching this regexp."), "max": Int("Max strings, default 400.")}, "path"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Path, Filter string
				Min, Max     int
			}
			Bind(raw, &a)
			p, err := env.Resolve(a.Path)
			if err != nil {
				return Errorf("%v", err)
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return Errorf("%v", err)
			}
			if a.Min <= 0 {
				a.Min = 5
			}
			if a.Max <= 0 {
				a.Max = 400
			}
			var filt *regexp.Regexp
			if a.Filter != "" {
				filt, err = regexp.Compile(a.Filter)
				if err != nil {
					return Errorf("bad filter: %v", err)
				}
			}
			out := extractStrings(b, a.Min, filt, a.Max)
			if len(out) == 0 {
				return Text("no matching strings")
			}
			return External("strings", strings.Join(out, "\n"))
		}})

	r.Add(&Tool{Name: "hexdump", Category: "reverse",
		Description: "Hex + ASCII dump of a byte range of a file, for inspecting headers and binary structures.",
		Schema:      Obj(map[string]any{"path": Str("File."), "offset": Int("Start byte, default 0."), "length": Int("Bytes to show, default 256 (max 4096).")}, "path"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Path           string
				Offset, Length int
			}
			Bind(raw, &a)
			p, err := env.Resolve(a.Path)
			if err != nil {
				return Errorf("%v", err)
			}
			f, err := os.Open(p)
			if err != nil {
				return Errorf("%v", err)
			}
			defer f.Close()
			if a.Length <= 0 || a.Length > 4096 {
				a.Length = 256
			}
			buf := make([]byte, a.Length)
			n, _ := f.ReadAt(buf, int64(a.Offset))
			return Text(hexDump(buf[:n], a.Offset))
		}})

	r.Add(&Tool{Name: "rea", Category: "reverse", Mutates: true,
		Description: `Reverse Engineer Anything (REA): deep analysis of shipped software — native binaries, JavaScript/Electron apps, .NET assemblies, packaged apps, firmware — to explain how a feature works from the artifact and reconstruct it. Runs the open-source rea-agents CLI (MIT, github.com/morluto/rea); analysis is local and evidence-backed.
action=check verifies rea is available; action=analyze-javascript runs static JS/Electron/ASAR analysis (no native engine needed); action=run passes raw arguments to the rea CLI. Read the reverse-engineer-anything skill first.`,
		Schema: Obj(map[string]any{"action": Enum("Operation.", "check", "analyze-javascript", "run"), "path": Str("Target path (app directory, ASAR, or file)."), "args": StrArr("Raw arguments for action=run.")}, "action"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Action, Path string
				Args         []string
			}
			Bind(raw, &a)
			node := lookTool(env, "node")
			npx := lookTool(env, "npx")
			if a.Action == "check" {
				reaBin := lookTool(env, "rea")
				var b strings.Builder
				fmt.Fprintf(&b, "node: %s\nnpx: %s\nrea (installed): %s\n", orNone(node), orNone(npx), orNone(reaBin))
				if reaBin == "" && npx == "" {
					b.WriteString("\nREA is not available. Install Node.js and run: npx -y rea-agents@latest setup\nThe reverse-engineer-anything skill explains connection and recovery.")
				} else {
					b.WriteString("\nREA can run via the installed CLI or npx. Read the reverse-engineer-anything skill before analysis.")
				}
				return Text(b.String())
			}
			var args []string
			switch a.Action {
			case "analyze-javascript":
				if a.Path == "" {
					return Errorf("path is required")
				}
				p, err := env.Resolve(a.Path)
				if err != nil {
					return Errorf("%v", err)
				}
				args = []string{"analyze-javascript-application", p, "--json"}
			case "run":
				args = a.Args
			default:
				return Errorf("unknown action")
			}
			bin, pre := lookTool(env, "rea"), []string{}
			if bin == "" {
				if npx == "" {
					return Errorf("neither rea nor npx is installed; install Node.js then `npx -y rea-agents@latest setup`")
				}
				bin, pre = npx, []string{"-y", "rea-agents@latest"}
			}
			full := append(pre, args...)
			return runCLI(ctx, env, "rea", bin, full, 1200)
		}})
}

func orNone(s string) string {
	if s == "" {
		return "(not found)"
	}
	return s
}

func InspectBinary(name string, b []byte, full bool) string {
	var out strings.Builder
	sum256 := sha256.Sum256(b)
	sum1 := sha1.Sum(b)
	sum5 := md5.Sum(b)
	fmt.Fprintf(&out, "File: %s\nSize: %d bytes\nMD5:    %s\nSHA1:   %s\nSHA256: %s\n", name, len(b), hex.EncodeToString(sum5[:]), hex.EncodeToString(sum1[:]), hex.EncodeToString(sum256[:]))
	format := detectFormat(b)
	fmt.Fprintf(&out, "Format: %s\n\n", format)
	switch {
	case bytes.HasPrefix(b, []byte("\x7fELF")):
		inspectELF(&out, b, full)
	case bytes.HasPrefix(b, []byte("MZ")):
		inspectPE(&out, b, full)
	case bytes.HasPrefix(b, []byte("\xfe\xed\xfa")) || bytes.HasPrefix(b, []byte("\xcf\xfa\xed\xfe")) || bytes.HasPrefix(b, []byte("\xca\xfe\xba\xbe")):
		inspectMachO(&out, b, full)
	default:
		out.WriteString("Not a native executable. Use strings_extract and hexdump, or the rea tool for JS/.NET/packaged apps.\n")
	}
	return out.String()
}

func detectFormat(b []byte) string {
	magics := []struct {
		sig  []byte
		name string
	}{
		{[]byte("\x7fELF"), "ELF executable"}, {[]byte("MZ"), "PE/DOS executable (Windows)"},
		{[]byte("\xca\xfe\xba\xbe"), "Mach-O universal / Java class"}, {[]byte("\xcf\xfa\xed\xfe"), "Mach-O 64-bit"},
		{[]byte("\xfe\xed\xfa\xce"), "Mach-O 32-bit"}, {[]byte("PK\x03\x04"), "ZIP/JAR/APK/ASAR-ish archive"},
		{[]byte("\x1f\x8b"), "gzip"}, {[]byte("BZh"), "bzip2"}, {[]byte("\xfd7zXZ"), "xz"},
		{[]byte("ustar"), "tar"}, {[]byte("%PDF"), "PDF"}, {[]byte("\x89PNG"), "PNG image"},
		{[]byte("\xff\xd8\xff"), "JPEG image"}, {[]byte("dex\n"), "Android DEX"}, {[]byte("\x04\x22\x4d\x18"), "LZ4"},
		{[]byte("!<arch>"), "ar/static library"}, {[]byte("\x42\x5a"), "possibly bzip2"},
	}
	for _, m := range magics {
		if bytes.HasPrefix(b, m.sig) {
			return m.name
		}
	}
	if isMostlyText(b) {
		return "text / source"
	}
	return "unknown binary"
}

func inspectELF(out *strings.Builder, b []byte, full bool) {
	f, err := elf.NewFile(bytes.NewReader(b))
	if err != nil {
		fmt.Fprintf(out, "ELF parse error: %v\n", err)
		return
	}
	defer f.Close()
	fmt.Fprintf(out, "Class: %s  Data: %s\nType: %s  Machine: %s\nEntry: 0x%x\n", f.Class, f.ByteOrder, f.Type, f.Machine, f.Entry)
	// Hardening
	nx, pie, relro, canary, bind := "disabled (RWX stack?)", "no (fixed load address)", "none", "not detected", ""
	for _, p := range f.Progs {
		if p.Type == elf.PT_GNU_STACK && p.Flags&elf.PF_X == 0 {
			nx = "enabled"
		}
		if p.Type == elf.PT_GNU_RELRO {
			relro = "partial"
		}
	}
	if f.Type == elf.ET_DYN {
		pie = "yes (PIE)"
	}
	syms, _ := f.DynamicSymbols()
	for _, s := range syms {
		if s.Name == "__stack_chk_fail" || s.Name == "__stack_chk_guard" {
			canary = "present (stack canary)"
		}
	}
	if ds := f.SectionByType(elf.SHT_DYNAMIC); ds != nil {
		if tags, _ := f.DynValue(elf.DT_FLAGS_1); len(tags) > 0 && tags[0]&0x8 != 0 {
			relro = "full"
			bind = "BIND_NOW"
		}
	}
	fmt.Fprintf(out, "\nHardening:\n  NX (no-exec stack): %s\n  PIE/ASLR: %s\n  RELRO: %s %s\n  Stack canary: %s\n", nx, pie, relro, bind, canary)
	libs, _ := f.ImportedLibraries()
	if len(libs) > 0 {
		fmt.Fprintf(out, "\nLinked libraries (%d): %s\n", len(libs), strings.Join(libs, ", "))
	}
	if interp := elfInterp(f); interp != "" {
		fmt.Fprintf(out, "Interpreter: %s\n", interp)
	}
	if full {
		fmt.Fprintf(out, "\nSections (%d):\n", len(f.Sections))
		for _, s := range f.Sections {
			fmt.Fprintf(out, "  %-20s %-14s addr=0x%-10x size=%d\n", s.Name, s.Type, s.Addr, s.Size)
		}
		imps, _ := f.ImportedSymbols()
		var names []string
		for _, s := range imps {
			names = append(names, s.Name)
		}
		if len(names) > 0 {
			fmt.Fprintf(out, "\nImported symbols (%d): %s\n", len(names), clip(strings.Join(names, ", "), 3000))
		}
	}
}

func elfInterp(f *elf.File) string {
	s := f.Section(".interp")
	if s == nil {
		return ""
	}
	d, _ := s.Data()
	return strings.TrimRight(string(d), "\x00")
}

func inspectPE(out *strings.Builder, b []byte, full bool) {
	f, err := pe.NewFile(bytes.NewReader(b))
	if err != nil {
		fmt.Fprintf(out, "PE parse error: %v\n", err)
		return
	}
	defer f.Close()
	arch := map[uint16]string{0x14c: "x86", 0x8664: "x64", 0xaa64: "ARM64", 0x1c0: "ARM"}[f.Machine]
	fmt.Fprintf(out, "Machine: %s (0x%x)  Sections: %d\n", arch, f.Machine, len(f.Sections))
	var dllChar uint16
	var subsystem uint16
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		dllChar, subsystem = oh.DllCharacteristics, oh.Subsystem
	case *pe.OptionalHeader32:
		dllChar, subsystem = oh.DllCharacteristics, oh.Subsystem
	}
	flag := func(bit uint16, yes string) string {
		if dllChar&bit != 0 {
			return yes
		}
		return "no"
	}
	fmt.Fprintf(out, "Subsystem: %s\nHardening:\n  ASLR (DYNAMIC_BASE): %s\n  DEP (NX_COMPAT): %s\n  High-entropy ASLR: %s\n  SEH disabled (NO_SEH): %s\n  Control Flow Guard: %s\n  Force integrity: %s\n",
		map[uint16]string{2: "GUI", 3: "console"}[subsystem],
		flag(0x40, "yes"), flag(0x100, "yes"), flag(0x20, "yes"), flag(0x400, "yes"), flag(0x4000, "yes"), flag(0x80, "yes"))
	libs, _ := f.ImportedLibraries()
	if len(libs) > 0 {
		fmt.Fprintf(out, "\nImported DLLs (%d): %s\n", len(libs), strings.Join(libs, ", "))
	}
	if full {
		imps, _ := f.ImportedSymbols()
		fmt.Fprintf(out, "\nImported symbols (%d): %s\n", len(imps), clip(strings.Join(imps, ", "), 3000))
		out.WriteString("\nSections:\n")
		for _, s := range f.Sections {
			fmt.Fprintf(out, "  %-10s vaddr=0x%-8x vsize=%d rawsize=%d\n", s.Name, s.VirtualAddress, s.VirtualSize, s.Size)
		}
	}
}

func inspectMachO(out *strings.Builder, b []byte, full bool) {
	f, err := macho.NewFile(bytes.NewReader(b))
	if err != nil {
		fmt.Fprintf(out, "Mach-O parse error (may be a universal/fat binary): %v\n", err)
		return
	}
	defer f.Close()
	fmt.Fprintf(out, "CPU: %s  Type: %s\nFlags: 0x%x (PIE: %v)\n", f.Cpu, f.Type, f.Flags, f.Flags&macho.FlagPIE != 0)
	libs, _ := f.ImportedLibraries()
	if len(libs) > 0 {
		fmt.Fprintf(out, "\nLinked libraries (%d): %s\n", len(libs), strings.Join(libs, ", "))
	}
	if full {
		syms, _ := f.ImportedSymbols()
		fmt.Fprintf(out, "\nImported symbols (%d): %s\n", len(syms), clip(strings.Join(syms, ", "), 3000))
		out.WriteString("\nSections:\n")
		for _, s := range f.Sections {
			fmt.Fprintf(out, "  %-16s %-16s addr=0x%-10x size=%d\n", s.Seg, s.Name, s.Addr, s.Size)
		}
	}
}

func extractStrings(b []byte, minLen int, filt *regexp.Regexp, max int) []string {
	var out []string
	seen := map[string]bool{}
	emit := func(s string) {
		if len(s) < minLen || seen[s] {
			return
		}
		if filt != nil && !filt.MatchString(s) {
			return
		}
		seen[s] = true
		if s, n := secure.Redact(s); n >= 0 {
			out = append(out, s)
		}
	}
	// ASCII runs
	var cur []byte
	for _, c := range b {
		if c >= 0x20 && c < 0x7f {
			cur = append(cur, c)
		} else {
			if len(cur) >= minLen {
				emit(string(cur))
			}
			cur = cur[:0]
		}
		if len(out) >= max {
			break
		}
	}
	if len(cur) >= minLen {
		emit(string(cur))
	}
	// UTF-16LE runs (common in PE)
	var u16 []byte
	for i := 0; i+1 < len(b) && len(out) < max; i += 2 {
		if b[i] >= 0x20 && b[i] < 0x7f && b[i+1] == 0 {
			u16 = append(u16, b[i])
		} else {
			if len(u16) >= minLen {
				emit(string(u16))
			}
			u16 = u16[:0]
		}
	}
	sort.Strings(out)
	return out
}

func hexDump(b []byte, base int) string {
	var out strings.Builder
	for i := 0; i < len(b); i += 16 {
		fmt.Fprintf(&out, "%08x  ", base+i)
		for j := 0; j < 16; j++ {
			if i+j < len(b) {
				fmt.Fprintf(&out, "%02x ", b[i+j])
			} else {
				out.WriteString("   ")
			}
			if j == 7 {
				out.WriteByte(' ')
			}
		}
		out.WriteString(" |")
		for j := 0; j < 16 && i+j < len(b); j++ {
			c := b[i+j]
			if c >= 0x20 && c < 0x7f {
				out.WriteByte(c)
			} else {
				out.WriteByte('.')
			}
		}
		out.WriteString("|\n")
	}
	return out.String()
}

func isMostlyText(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	n := min(len(b), 2000)
	printable := 0
	for _, c := range b[:n] {
		if c == '\n' || c == '\r' || c == '\t' || (c >= 0x20 && c < 0x7f) {
			printable++
		}
	}
	return float64(printable)/float64(n) > 0.9
}


var _ = context.Background
