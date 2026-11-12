package md2docx

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// imageRef 是 {{ s.img }} 的求值结果，渲染阶段会落成真正的图片段落。
type imageRef struct {
	path string
}

type valueCtx struct {
	vars map[string]any
	data *DocData
}

func newValueCtx(data *DocData) *valueCtx {
	return &valueCtx{vars: map[string]any{}, data: data}
}

func (c *valueCtx) child(name string, value any) *valueCtx {
	vars := make(map[string]any, len(c.vars)+1)
	for k, v := range c.vars {
		vars[k] = v
	}
	vars[name] = value
	return &valueCtx{vars: vars, data: c.data}
}

// eval 求值模板表达式，覆盖报告模板实际用到的 Jinja 子集：
// 变量、点号/下标、|length、比较、and/or/not、三元表达式。
func (c *valueCtx) eval(expr string) any {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}
	if strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") && balanced(expr) {
		return c.eval(expr[1 : len(expr)-1])
	}
	if idx, ok := findTopWord(expr, "if"); ok {
		if elseIdx, ok2 := findTopWord(expr[idx+2:], "else"); ok2 {
			elseIdx += idx + 2
			cond := expr[idx+2 : elseIdx]
			if truthy(c.eval(cond)) {
				return c.eval(expr[:idx])
			}
			return c.eval(expr[elseIdx+4:])
		}
	}
	if idx, ok := findTopWordLast(expr, "or"); ok {
		return truthy(c.eval(expr[:idx])) || truthy(c.eval(expr[idx+2:]))
	}
	if idx, ok := findTopWordLast(expr, "and"); ok {
		return truthy(c.eval(expr[:idx])) && truthy(c.eval(expr[idx+3:]))
	}
	if strings.HasPrefix(expr, "not ") {
		return !truthy(c.eval(expr[4:]))
	}
	if op, idx := findComparison(expr); idx >= 0 {
		return compare(c.eval(expr[:idx]), op, c.eval(expr[idx+len(op):]))
	}
	if idx, ok := findTopPipe(expr); ok {
		left := c.eval(expr[:idx])
		switch strings.TrimSpace(expr[idx+1:]) {
		case "length":
			return lengthOf(left)
		default:
			return left
		}
	}
	return c.resolveValue(expr)
}

func balanced(s string) bool {
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '(':
			depth++
		case ')':
			depth--
		}
	}
	return depth == 0
}

func findTopWord(s, word string) (int, bool) {
	return scanTop(s, func(i, depth int) bool {
		if depth != 0 || !strings.HasPrefix(s[i:], word) {
			return false
		}
		beforeOK := i == 0 || isXMLSpace(s[i-1])
		j := i + len(word)
		afterOK := j == len(s) || isXMLSpace(s[j])
		return beforeOK && afterOK
	})
}

func findTopWordLast(s, word string) (int, bool) {
	last := -1
	offset := 0
	for {
		idx, ok := findTopWord(s[offset:], word)
		if !ok {
			break
		}
		last = offset + idx
		offset += idx + len(word)
	}
	if last < 0 {
		return 0, false
	}
	return last, true
}

func findTopPipe(s string) (int, bool) {
	return scanTop(s, func(i, depth int) bool {
		return depth == 0 && s[i] == '|'
	})
}

func findComparison(s string) (string, int) {
	idx := -1
	op := ""
	scanTop(s, func(i, depth int) bool {
		if depth != 0 {
			return false
		}
		for _, candidate := range []string{"==", "!=", ">=", "<=", ">", "<"} {
			if strings.HasPrefix(s[i:], candidate) {
				op, idx = candidate, i
				return true
			}
		}
		return false
	})
	return op, idx
}

// scanTop 只在括号/引号之外的字符上回调，命中后立即停止。
func scanTop(s string, hit func(i, depth int) bool) (int, bool) {
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		default:
			if depth == 0 && hit(i, depth) {
				return i, true
			}
		}
	}
	return 0, false
}

func (c *valueCtx) resolveValue(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		if v, err := strconv.Unquote(s); err == nil {
			return v
		}
		return s[1 : len(s)-1]
	}
	switch s {
	case "true", "True":
		return true
	case "false", "False":
		return false
	case "none", "None", "null", "nil":
		return nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	if strings.HasSuffix(s, ".img") {
		if base := c.resolveValue(strings.TrimSuffix(s, ".img")); base != nil {
			if shot, ok := base.(Shot); ok {
				return imageRef{path: shot.Path}
			}
		}
	}
	return c.lookupPath(s)
}

func (c *valueCtx) lookupPath(path string) any {
	segs, ok := splitPath(path)
	if !ok || len(segs) == 0 {
		return nil
	}
	var cur any
	if v, ok := c.vars[segs[0].name]; ok {
		cur = v
	} else {
		cur = fieldByName(c.data, segs[0].name)
	}
	for _, seg := range segs[1:] {
		if seg.index != nil {
			cur = indexValue(cur, c.eval(*seg.index))
			continue
		}
		cur = fieldByName(cur, seg.name)
		if cur == nil {
			return nil
		}
	}
	return cur
}

type pathSeg struct {
	name  string
	index *string
}

func splitPath(path string) ([]pathSeg, bool) {
	var out []pathSeg
	i := 0
	for i < len(path) && isXMLSpace(path[i]) {
		i++
	}
	start := i
	for i < len(path) && isIdentByte(path[i]) {
		i++
	}
	if start == i {
		return nil, false
	}
	out = append(out, pathSeg{name: path[start:i]})
	for i < len(path) {
		switch path[i] {
		case '.':
			i++
			start = i
			for i < len(path) && isIdentByte(path[i]) {
				i++
			}
			if start == i {
				return out, true
			}
			out = append(out, pathSeg{name: path[start:i]})
		case '[':
			j := strings.IndexByte(path[i:], ']')
			if j < 0 {
				return out, true
			}
			inner := strings.TrimSpace(path[i+1 : i+j])
			innerCopy := inner
			out = append(out, pathSeg{index: &innerCopy})
			i += j + 1
		default:
			return out, true
		}
	}
	return out, true
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func fieldByName(v any, name string) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if name == "" {
		return nil
	}
	switch rv.Kind() {
	case reflect.Struct:
		rt := rv.Type()
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if f.PkgPath != "" {
				continue
			}
			if f.Name == name || strings.EqualFold(f.Name, name) || jsonName(f) == name {
				return rv.Field(i).Interface()
			}
		}
	case reflect.Map:
		key := reflect.ValueOf(name)
		if key.Type().AssignableTo(rv.Type().Key()) {
			mv := rv.MapIndex(key)
			if mv.IsValid() {
				return mv.Interface()
			}
		}
	}
	return nil
}

func jsonName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return ""
	}
	name := strings.Split(tag, ",")[0]
	if name == "-" {
		return ""
	}
	return name
}

func indexValue(v any, idx any) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		n, ok := toInt(idx)
		if !ok || n < 0 || n >= rv.Len() {
			return nil
		}
		return rv.Index(n).Interface()
	case reflect.Map:
		key := reflect.ValueOf(fmt.Sprint(idx))
		if key.Type().AssignableTo(rv.Type().Key()) {
			mv := rv.MapIndex(key)
			if mv.IsValid() {
				return mv.Interface()
			}
		}
	}
	return nil
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		return i, err == nil
	}
	return 0, false
}

func truthy(v any) bool {
	if v == nil {
		return false
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return false
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Bool:
		return rv.Bool()
	case reflect.String:
		return rv.String() != ""
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return rv.Uint() != 0
	case reflect.Float32, reflect.Float64:
		return rv.Float() != 0
	case reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len() > 0
	}
	return true
}

func lengthOf(v any) int {
	if v == nil {
		return 0
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return 0
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.String:
		return len([]rune(rv.String()))
	case reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len()
	}
	return 0
}

func compare(left any, op string, right any) bool {
	lf, lok := number(left)
	rf, rok := number(right)
	var cmp int
	switch {
	case lok && rok:
		switch {
		case lf < rf:
			cmp = -1
		case lf > rf:
			cmp = 1
		}
	default:
		ls, rs := displayValue(left), displayValue(right)
		cmp = strings.Compare(ls, rs)
	}
	switch op {
	case "==":
		return cmp == 0
	case "!=":
		return cmp != 0
	case ">":
		return cmp > 0
	case "<":
		return cmp < 0
	case ">=":
		return cmp >= 0
	case "<=":
		return cmp <= 0
	}
	return false
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// displayValue 把标量值转成写入 Word 的文字。
func displayValue(v any) string {
	if v == nil {
		return ""
	}
	switch n := v.(type) {
	case string:
		return n
	case imageRef:
		return ""
	case bool:
		if n {
			return "True"
		}
		return "False"
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return ""
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.String:
		return rv.String()
	case reflect.Bool:
		return strconv.FormatBool(rv.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10)
	}
	return ""
}
