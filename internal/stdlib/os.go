package stdlib

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	rt "vexlua/internal/runtime"
)

func registerOS(runtime *rt.Runtime) error {
	handle := runtime.Heap().NewTable(12)
	table := runtime.Heap().Table(handle)
	startedAt := time.Now()

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
		cmd := exec.Command("cmd", "/C", command)
		err := cmd.Run()
		if err == nil {
			return rt.NumberValue(0), nil
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return rt.NumberValue(float64(exitErr.ExitCode())), nil
		}
		return rt.NumberValue(-1), nil
	}))
	table.SetSymbol(runtime.InternSymbol("exit"), runtime.NewHostFunction("os.exit", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		return rt.NilValue, fmt.Errorf("os.exit is not supported inside vexlua")
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
		if len(args) == 0 || args[0].Kind() == rt.KindNil {
			return runtime.StringValue("C"), nil
		}
		locale, ok := runtime.ToString(args[0])
		if !ok {
			return rt.NilValue, fmt.Errorf("os.setlocale expects string locale")
		}
		if locale == "C" || locale == "POSIX" || locale == "" {
			return runtime.StringValue(locale), nil
		}
		return rt.NilValue, nil
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

func formatDate(when time.Time, format string) string {
	var builder strings.Builder
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			builder.WriteByte(format[i])
			continue
		}
		i++
		switch format[i] {
		case 'a':
			builder.WriteString(when.Format("Mon"))
		case 'A':
			builder.WriteString(when.Format("Monday"))
		case 'b':
			builder.WriteString(when.Format("Jan"))
		case 'B':
			builder.WriteString(when.Format("January"))
		case 'c':
			builder.WriteString(when.Format("Mon Jan _2 15:04:05 2006"))
		case 'd':
			builder.WriteString(fmt.Sprintf("%02d", when.Day()))
		case 'H':
			builder.WriteString(fmt.Sprintf("%02d", when.Hour()))
		case 'I':
			hour := when.Hour() % 12
			if hour == 0 {
				hour = 12
			}
			builder.WriteString(fmt.Sprintf("%02d", hour))
		case 'j':
			builder.WriteString(fmt.Sprintf("%03d", when.YearDay()))
		case 'm':
			builder.WriteString(fmt.Sprintf("%02d", when.Month()))
		case 'M':
			builder.WriteString(fmt.Sprintf("%02d", when.Minute()))
		case 'p':
			builder.WriteString(when.Format("PM"))
		case 'S':
			builder.WriteString(fmt.Sprintf("%02d", when.Second()))
		case 'w':
			builder.WriteString(fmt.Sprintf("%d", when.Weekday()))
		case 'x':
			builder.WriteString(when.Format("01/02/06"))
		case 'X':
			builder.WriteString(when.Format("15:04:05"))
		case 'y':
			builder.WriteString(fmt.Sprintf("%02d", when.Year()%100))
		case 'Y':
			builder.WriteString(fmt.Sprintf("%04d", when.Year()))
		case 'Z':
			builder.WriteString(when.Format("MST"))
		case '%':
			builder.WriteByte('%')
		default:
			builder.WriteByte('%')
			builder.WriteByte(format[i])
		}
	}
	return builder.String()
}
