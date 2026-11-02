package scripts

import "strings"

// statement là một câu SQL đã bỏ chú thích; chuỗi ký tự giữ nguyên chỗ nhưng được thay bằng khoảng trắng
// để regex lint không khớp nhầm vào nội dung chuỗi hoặc thân hàm ($$...$$).
type statement struct {
	text string // đã bỏ chú thích/chuỗi, chữ thường, gom khoảng trắng
	line int    // dòng bắt đầu (1-based)
}

// splitStatements tách theo ';' ngoài chuỗi '...', "..." (giữ tên), $tag$...$tag$ và chú thích.
func splitStatements(sql string) []statement {
	var out []statement
	var cur strings.Builder
	line, start := 1, 1
	emit := func() {
		t := strings.Join(strings.Fields(strings.ToLower(cur.String())), " ")
		if t != "" {
			out = append(out, statement{text: t, line: start})
		}
		cur.Reset()
	}
	n := len(sql)
	for i := 0; i < n; i++ {
		c := sql[i]
		if c == '\n' {
			line++
		}
		if cur.Len() == 0 || strings.TrimSpace(cur.String()) == "" {
			start = line
		}
		switch {
		case c == '-' && i+1 < n && sql[i+1] == '-':
			for i < n && sql[i] != '\n' {
				i++
			}
			if i < n {
				line++
				cur.WriteByte(' ')
			}
		case c == '/' && i+1 < n && sql[i+1] == '*':
			i += 2
			for i+1 < n && !(sql[i] == '*' && sql[i+1] == '/') {
				if sql[i] == '\n' {
					line++
				}
				i++
			}
			i++
			cur.WriteByte(' ')
		case c == '\'':
			i++
			for i < n {
				if sql[i] == '\n' {
					line++
				}
				if sql[i] == '\'' {
					if i+1 < n && sql[i+1] == '\'' {
						i += 2
						continue
					}
					break
				}
				i++
			}
			cur.WriteString("''")
		case c == '$':
			j := i + 1
			for j < n && (sql[j] == '_' || sql[j] >= 'a' && sql[j] <= 'z' || sql[j] >= 'A' && sql[j] <= 'Z' || sql[j] >= '0' && sql[j] <= '9') {
				j++
			}
			if j < n && sql[j] == '$' {
				tag := sql[i : j+1]
				end := strings.Index(sql[j+1:], tag)
				if end < 0 {
					end = n - j - 1
				}
				body := sql[j+1 : j+1+end]
				line += strings.Count(body, "\n")
				i = j + 1 + end + len(tag) - 1
				cur.WriteString(" $body$ ")
			} else {
				cur.WriteByte(c)
			}
		case c == ';':
			emit()
		default:
			cur.WriteByte(c)
		}
	}
	emit()
	return out
}
