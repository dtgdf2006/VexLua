package stdlib

import (
	"fmt"
	"os"
	goruntime "runtime"
	"strings"
	"time"

	rt "vexlua/internal/runtime"
)

type localeState struct {
	system   string
	values   map[string]string
	category []string
}

func registerOS(runtime *rt.Runtime) error {
	handle := runtime.Heap().NewTable(12)
	table := runtime.Heap().Table(handle)
	startedAt := time.Now()
	locales := newLocaleState()

	table.SetSymbol(runtime.InternSymbol("clock"), runtime.NewHostFunction("os.clock", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 0 {
			return rt.NilValue, fmt.Errorf("os.clock expects no arguments")
		}
		return rt.NumberValue(time.Since(startedAt).Seconds()), nil
	}))
	table.SetSymbol(runtime.InternSymbol("difftime"), runtime.NewHostFunction("os.difftime", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) == 0 || len(args) > 2 {
			return rt.NilValue, fmt.Errorf("os.difftime expects 1 or 2 arguments")
		}
		if !args[0].IsNumber() {
			return rt.NilValue, fmt.Errorf("os.difftime expects numeric time")
		}
		seconds := args[0].Number()
		base := 0.0
		if len(args) > 1 {
			if !args[1].IsNumber() {
				return rt.NilValue, fmt.Errorf("os.difftime expects numeric time")
			}
			base = args[1].Number()
		}
		return rt.NumberValue(seconds - base), nil
	}))
	table.SetSymbol(runtime.InternSymbol("execute"), runtime.NewHostFunction("os.execute", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) > 1 {
			return rt.NilValue, fmt.Errorf("os.execute expects 0 or 1 argument")
		}
		if len(args) == 0 || args[0].Kind() == rt.KindNil {
			return rt.NumberValue(0), nil
		}
		command, ok := runtime.ToString(args[0])
		if !ok {
			return rt.NilValue, fmt.Errorf("os.execute expects string command")
		}
		cmd := shellCommand(command)
		err := cmd.Run()
		if err == nil {
			return rt.NumberValue(0), nil
		}
		if code, ok := failureCode(err); ok {
			return rt.NumberValue(float64(code)), nil
		}
		return rt.NumberValue(-1), nil
	}))
	table.SetSymbol(runtime.InternSymbol("exit"), runtime.NewHostFunction("os.exit", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) > 1 {
			return rt.NilValue, fmt.Errorf("os.exit expects 0 or 1 argument")
		}
		code := 0
		if len(args) == 1 && args[0].Kind() != rt.KindNil {
			if !args[0].IsNumber() {
				return rt.NilValue, fmt.Errorf("os.exit expects numeric code")
			}
			code = int(args[0].Number())
		}
		raiseExit(code)
		return rt.NilValue, nil
	}))
	table.SetSymbol(runtime.InternSymbol("getenv"), runtime.NewHostFunction("os.getenv", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("os.getenv expects 1 argument")
		}
		name, ok := runtime.ToString(args[0])
		if !ok {
			return rt.NilValue, fmt.Errorf("os.getenv expects string name")
		}
		value, ok := os.LookupEnv(name)
		if !ok {
			return rt.NilValue, nil
		}
		return runtime.StringValue(value), nil
	}))
	table.SetSymbol(runtime.InternSymbol("remove"), runtime.NewHostFunctionMulti("os.remove", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("os.remove expects 1 argument")
		}
		name, ok := runtime.ToString(args[0])
		if !ok {
			return nil, fmt.Errorf("os.remove expects string filename")
		}
		if err := os.Remove(name); err != nil {
			return failureValues(runtime, err), nil
		}
		return []rt.Value{rt.TrueValue}, nil
	}))
	table.SetSymbol(runtime.InternSymbol("rename"), runtime.NewHostFunctionMulti("os.rename", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("os.rename expects 2 arguments")
		}
		fromName, ok := runtime.ToString(args[0])
		if !ok {
			return nil, fmt.Errorf("os.rename expects string source")
		}
		toName, ok := runtime.ToString(args[1])
		if !ok {
			return nil, fmt.Errorf("os.rename expects string target")
		}
		if err := os.Rename(fromName, toName); err != nil {
			return failureValues(runtime, err), nil
		}
		return []rt.Value{rt.TrueValue}, nil
	}))
	table.SetSymbol(runtime.InternSymbol("setlocale"), runtime.NewHostFunction("os.setlocale", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) > 2 {
			return rt.NilValue, fmt.Errorf("os.setlocale expects up to 2 arguments")
		}
		category := "all"
		if len(args) > 1 && args[1].Kind() != rt.KindNil {
			text, ok := runtime.ToString(args[1])
			if !ok {
				return rt.NilValue, fmt.Errorf("os.setlocale expects string category")
			}
			if !locales.validCategory(text) {
				return rt.NilValue, fmt.Errorf("invalid locale category %q", text)
			}
			category = text
		}
		if len(args) == 0 || args[0].Kind() == rt.KindNil {
			return runtime.StringValue(locales.query(category)), nil
		}
		locale, ok := runtime.ToString(args[0])
		if !ok {
			return rt.NilValue, fmt.Errorf("os.setlocale expects string locale")
		}
		value, changed := locales.apply(locale, category)
		if !changed {
			return rt.NilValue, nil
		}
		return runtime.StringValue(value), nil
	}))
	table.SetSymbol(runtime.InternSymbol("time"), runtime.NewHostFunction("os.time", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) > 1 {
			return rt.NilValue, fmt.Errorf("os.time expects 0 or 1 argument")
		}
		if len(args) == 0 || args[0].Kind() == rt.KindNil {
			return rt.NumberValue(float64(time.Now().Unix())), nil
		}
		tableValue, err := asTable(runtime, args[0])
		if err != nil {
			return rt.NilValue, err
		}
		day, ok, err := intField(runtime, tableValue, "day", true, 0)
		if err != nil {
			return rt.NilValue, err
		}
		if !ok {
			return rt.NilValue, nil
		}
		month, ok, err := intField(runtime, tableValue, "month", true, 0)
		if err != nil {
			return rt.NilValue, err
		}
		if !ok {
			return rt.NilValue, nil
		}
		year, ok, err := intField(runtime, tableValue, "year", true, 0)
		if err != nil {
			return rt.NilValue, err
		}
		if !ok {
			return rt.NilValue, nil
		}
		hour, _, err := intField(runtime, tableValue, "hour", false, 12)
		if err != nil {
			return rt.NilValue, err
		}
		minute, _, err := intField(runtime, tableValue, "min", false, 0)
		if err != nil {
			return rt.NilValue, err
		}
		second, _, err := intField(runtime, tableValue, "sec", false, 0)
		if err != nil {
			return rt.NilValue, err
		}
		stamp := time.Date(year, time.Month(month), day, hour, minute, second, 0, time.Local)
		return rt.NumberValue(float64(stamp.Unix())), nil
	}))
	table.SetSymbol(runtime.InternSymbol("date"), runtime.NewHostFunction("os.date", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) > 2 {
			return rt.NilValue, fmt.Errorf("os.date expects up to 2 arguments")
		}
		format := "%c"
		if len(args) > 0 && args[0].Kind() != rt.KindNil {
			text, ok := runtime.ToString(args[0])
			if !ok {
				return rt.NilValue, fmt.Errorf("os.date expects string format")
			}
			format = text
		}
		stamp := time.Now().Unix()
		if len(args) > 1 && args[1].Kind() != rt.KindNil {
			if !args[1].IsNumber() {
				return rt.NilValue, fmt.Errorf("os.date expects numeric time")
			}
			stamp = int64(args[1].Number())
		}
		utc := false
		if strings.HasPrefix(format, "!") {
			utc = true
			format = format[1:]
		}
		when := time.Unix(stamp, 0)
		if utc {
			when = when.UTC()
		} else {
			when = when.Local()
		}
		if format == "*t" {
			h := runtime.Heap().NewTable(9)
			dateTable := runtime.Heap().Table(h)
			dateTable.SetSymbol(runtime.InternSymbol("sec"), rt.NumberValue(float64(when.Second())))
			dateTable.SetSymbol(runtime.InternSymbol("min"), rt.NumberValue(float64(when.Minute())))
			dateTable.SetSymbol(runtime.InternSymbol("hour"), rt.NumberValue(float64(when.Hour())))
			dateTable.SetSymbol(runtime.InternSymbol("day"), rt.NumberValue(float64(when.Day())))
			dateTable.SetSymbol(runtime.InternSymbol("month"), rt.NumberValue(float64(when.Month())))
			dateTable.SetSymbol(runtime.InternSymbol("year"), rt.NumberValue(float64(when.Year())))
			dateTable.SetSymbol(runtime.InternSymbol("wday"), rt.NumberValue(float64(when.Weekday())+1))
			dateTable.SetSymbol(runtime.InternSymbol("yday"), rt.NumberValue(float64(when.YearDay())))
			dateTable.SetSymbol(runtime.InternSymbol("isdst"), rt.BoolValue(when.IsDST()))
			return rt.HandleValue(h), nil
		}
		return runtime.StringValue(formatDate(when, format)), nil
	}))
	table.SetSymbol(runtime.InternSymbol("tmpname"), runtime.NewHostFunction("os.tmpname", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 0 {
			return rt.NilValue, fmt.Errorf("os.tmpname expects no arguments")
		}
		file, err := os.CreateTemp("", "vexlua-*")
		if err != nil {
			return rt.NilValue, err
		}
		name := file.Name()
		_ = file.Close()
		_ = os.Remove(name)
		return runtime.StringValue(name), nil
	}))

	runtime.SetGlobal("os", rt.HandleValue(handle))
	return nil
}

func intField(runtime *rt.Runtime, table *rt.Table, name string, required bool, defaultValue int) (int, bool, error) {
	value, _, found := table.GetSymbol(runtime.InternSymbol(name))
	if !found || value.Kind() == rt.KindNil {
		if required {
			return 0, false, nil
		}
		return defaultValue, true, nil
	}
	if !value.IsNumber() {
		return 0, false, fmt.Errorf("field %s must be numeric", name)
	}
	return int(value.Number()), true, nil
}

func newLocaleState() *localeState {
	return &localeState{
		system: detectSystemLocale(),
		values: map[string]string{
			"collate":  "C",
			"ctype":    "C",
			"monetary": "C",
			"numeric":  "C",
			"time":     "C",
		},
		category: []string{"collate", "ctype", "monetary", "numeric", "time"},
	}
}

func (s *localeState) validCategory(category string) bool {
	if category == "all" {
		return true
	}
	for _, name := range s.category {
		if category == name {
			return true
		}
	}
	return false
}

func (s *localeState) query(category string) string {
	if category != "all" {
		return s.values[category]
	}
	first := s.values[s.category[0]]
	for _, name := range s.category[1:] {
		if s.values[name] != first {
			parts := make([]string, 0, len(s.category))
			for _, item := range s.category {
				parts = append(parts, item+"="+s.values[item])
			}
			return strings.Join(parts, ";")
		}
	}
	return first
}

func (s *localeState) apply(locale string, category string) (string, bool) {
	resolved, ok := s.resolve(locale)
	if !ok {
		return "", false
	}
	if category == "all" {
		for _, name := range s.category {
			s.values[name] = resolved
		}
		return resolved, true
	}
	s.values[category] = resolved
	return resolved, true
}

func (s *localeState) resolve(locale string) (string, bool) {
	switch locale {
	case "C":
		return "C", true
	case "POSIX":
		if goruntime.GOOS == "windows" {
			return "", false
		}
		return "C", true
	case "":
		return s.system, true
	}
	if locale == s.system || looksLikeLocaleName(locale) {
		return locale, true
	}
	return "", false
}

func detectSystemLocale() string {
	for _, key := range []string{"LC_ALL", "LC_TIME", "LANG"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return "C"
}

func looksLikeLocaleName(locale string) bool {
	if strings.ContainsAny(locale, " ()") {
		return looksLikeWindowsLocaleName(locale)
	}
	normalized := locale
	if dot := strings.IndexByte(normalized, '.'); dot >= 0 {
		normalized = normalized[:dot]
	}
	if at := strings.IndexByte(normalized, '@'); at >= 0 {
		normalized = normalized[:at]
	}
	normalized = strings.ReplaceAll(normalized, "_", "-")
	parts := strings.Split(normalized, "-")
	if len(parts) == 0 || !isAlphaPart(parts[0], 2, 3) {
		return false
	}
	for index, part := range parts[1:] {
		switch {
		case index == 0 && isAlphaPart(part, 4, 4):
		case index == 0 && (isAlphaPart(part, 2, 2) || isDigitPart(part, 3, 3)):
		case isAlphaNumPart(part, 4, 8):
		default:
			return false
		}
	}
	return true
}

func looksLikeWindowsLocaleName(locale string) bool {
	if !strings.Contains(locale, "_") {
		return false
	}
	for i := 0; i < len(locale); i++ {
		ch := locale[i]
		if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			continue
		}
		switch ch {
		case ' ', '(', ')', '_', '-', '.':
			continue
		default:
			return false
		}
	}
	return true
}

func isAlphaPart(text string, minLen int, maxLen int) bool {
	if len(text) < minLen || len(text) > maxLen {
		return false
	}
	for i := 0; i < len(text); i++ {
		if (text[i] < 'A' || text[i] > 'Z') && (text[i] < 'a' || text[i] > 'z') {
			return false
		}
	}
	return true
}

func isDigitPart(text string, minLen int, maxLen int) bool {
	if len(text) < minLen || len(text) > maxLen {
		return false
	}
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}

func isAlphaNumPart(text string, minLen int, maxLen int) bool {
	if len(text) < minLen || len(text) > maxLen {
		return false
	}
	for i := 0; i < len(text); i++ {
		if (text[i] < 'A' || text[i] > 'Z') && (text[i] < 'a' || text[i] > 'z') && (text[i] < '0' || text[i] > '9') {
			return false
		}
	}
	return true
}

func formatDate(when time.Time, format string) string {
	var builder strings.Builder
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			builder.WriteByte(format[i])
			continue
		}
		i++
		builder.WriteString(formatDateDirective(when, format[i]))
	}
	return builder.String()
}

func formatDateDirective(when time.Time, directive byte) string {
	shortWeekday := [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	fullWeekday := [...]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	shortMonth := [...]string{"", "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	fullMonth := [...]string{"", "January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
	switch directive {
	case 'a':
		return shortWeekday[when.Weekday()]
	case 'A':
		return fullWeekday[when.Weekday()]
	case 'b':
		return shortMonth[int(when.Month())]
	case 'B':
		return fullMonth[int(when.Month())]
	case 'c':
		return formatDate(when, "%x %X")
	case 'C':
		return fmt.Sprintf("%02d", when.Year()/100)
	case 'd':
		return fmt.Sprintf("%02d", when.Day())
	case 'D':
		return formatDate(when, "%m/%d/%y")
	case 'e':
		return fmt.Sprintf("%2d", when.Day())
	case 'g':
		isoYear, _ := when.ISOWeek()
		return fmt.Sprintf("%02d", isoYear%100)
	case 'G':
		isoYear, _ := when.ISOWeek()
		return fmt.Sprintf("%04d", isoYear)
	case 'h':
		return shortMonth[int(when.Month())]
	case 'H':
		return fmt.Sprintf("%02d", when.Hour())
	case 'I':
		hour := when.Hour() % 12
		if hour == 0 {
			hour = 12
		}
		return fmt.Sprintf("%02d", hour)
	case 'j':
		return fmt.Sprintf("%03d", when.YearDay())
	case 'm':
		return fmt.Sprintf("%02d", when.Month())
	case 'M':
		return fmt.Sprintf("%02d", when.Minute())
	case 'n':
		return "\n"
	case 'p':
		if when.Hour() < 12 {
			return "AM"
		}
		return "PM"
	case 'r':
		return formatDate(when, "%I:%M:%S %p")
	case 'R':
		return formatDate(when, "%H:%M")
	case 'S':
		return fmt.Sprintf("%02d", when.Second())
	case 't':
		return "\t"
	case 'T':
		return formatDate(when, "%H:%M:%S")
	case 'u':
		weekday := int(when.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		return fmt.Sprintf("%d", weekday)
	case 'U':
		return fmt.Sprintf("%02d", weekNumber(when, time.Sunday))
	case 'V':
		_, isoWeek := when.ISOWeek()
		return fmt.Sprintf("%02d", isoWeek)
	case 'w':
		return fmt.Sprintf("%d", when.Weekday())
	case 'W':
		return fmt.Sprintf("%02d", weekNumber(when, time.Monday))
	case 'x':
		return fmt.Sprintf("%02d/%02d/%02d", int(when.Month()), when.Day(), when.Year()%100)
	case 'X':
		return formatDate(when, "%H:%M:%S")
	case 'y':
		return fmt.Sprintf("%02d", when.Year()%100)
	case 'Y':
		return fmt.Sprintf("%04d", when.Year())
	case 'z':
		_, offset := when.Zone()
		sign := '+'
		if offset < 0 {
			sign = '-'
			offset = -offset
		}
		return fmt.Sprintf("%c%02d%02d", sign, offset/3600, (offset%3600)/60)
	case 'Z':
		name, _ := when.Zone()
		return name
	case '%':
		return "%"
	default:
		return "%" + string(directive)
	}
}

func weekNumber(when time.Time, firstWeekday time.Weekday) int {
	jan1 := time.Date(when.Year(), time.January, 1, 0, 0, 0, 0, when.Location())
	firstOffset := (7 + int(firstWeekday) - int(jan1.Weekday())) % 7
	yday := when.YearDay() - 1
	if yday < firstOffset {
		return 0
	}
	return 1 + (yday-firstOffset)/7
}
