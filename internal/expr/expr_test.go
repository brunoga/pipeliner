package expr

import (
	"testing"
	"time"
)

func eval(t *testing.T, expression string, data map[string]any) bool {
	t.Helper()
	e, err := Compile(expression)
	if err != nil {
		t.Fatalf("Compile(%q): %v", expression, err)
	}
	result, err := e.Eval(data)
	if err != nil {
		t.Fatalf("Eval(%q): %v", expression, err)
	}
	return result
}

func TestNumericComparisons(t *testing.T) {
	data := map[string]any{"score": 7.5}
	if !eval(t, "score > 7.0", data) {
		t.Error("7.5 > 7.0 should be true")
	}
	if eval(t, "score > 8.0", data) {
		t.Error("7.5 > 8.0 should be false")
	}
	if !eval(t, "score >= 7.5", data) {
		t.Error("7.5 >= 7.5 should be true")
	}
	if !eval(t, "score < 8.0", data) {
		t.Error("7.5 < 8.0 should be true")
	}
	if !eval(t, "score <= 7.5", data) {
		t.Error("7.5 <= 7.5 should be true")
	}
	if !eval(t, "score == 7.5", data) {
		t.Error("7.5 == 7.5 should be true")
	}
	if eval(t, "score != 7.5", data) {
		t.Error("7.5 != 7.5 should be false")
	}
}

func TestStringComparisons(t *testing.T) {
	data := map[string]any{"source": "CAM"}
	if !eval(t, `source == "CAM"`, data) {
		t.Error(`"CAM" == "CAM" should be true`)
	}
	if eval(t, `source == "HD"`, data) {
		t.Error(`"CAM" == "HD" should be false`)
	}
	if !eval(t, `source != "HD"`, data) {
		t.Error(`"CAM" != "HD" should be true`)
	}
}

func TestStringContains(t *testing.T) {
	data := map[string]any{"genre": "action comedy"}
	if !eval(t, `genre contains "action"`, data) {
		t.Error("should contain 'action'")
	}
	if eval(t, `genre contains "drama"`, data) {
		t.Error("should not contain 'drama'")
	}
}

func TestStringMatches(t *testing.T) {
	data := map[string]any{"title": "The Matrix 1999"}
	if !eval(t, `title matches "Matrix"`, data) {
		t.Error("should match 'Matrix'")
	}
	if !eval(t, `title matches "\\d{4}"`, data) {
		t.Error("should match year pattern")
	}
	if eval(t, `title matches "^1999"`, data) {
		t.Error("should not match '^1999'")
	}
}

func TestLogicalAnd(t *testing.T) {
	data := map[string]any{"score": 8.0, "source": "HD"}
	if !eval(t, `score > 7.0 and source == "HD"`, data) {
		t.Error("both true: should be true")
	}
	if eval(t, `score > 7.0 and source == "CAM"`, data) {
		t.Error("second false: should be false")
	}
	// && syntax
	if !eval(t, `score > 7.0 && source == "HD"`, data) {
		t.Error("&& syntax: should be true")
	}
}

func TestLogicalOr(t *testing.T) {
	data := map[string]any{"score": 4.0, "source": "HD"}
	if !eval(t, `score > 7.0 or source == "HD"`, data) {
		t.Error("second true: should be true")
	}
	if eval(t, `score > 7.0 or source == "CAM"`, data) {
		t.Error("both false: should be false")
	}
	// || syntax
	if !eval(t, `score > 3.0 || source == "CAM"`, data) {
		t.Error("|| syntax first true: should be true")
	}
}

func TestLogicalNot(t *testing.T) {
	data := map[string]any{"source": "CAM"}
	if !eval(t, `not source == "HD"`, data) {
		t.Error("not false: should be true")
	}
	if eval(t, `not source == "CAM"`, data) {
		t.Error("not true: should be false")
	}
	// ! syntax
	if !eval(t, `! source == "HD"`, data) {
		t.Error("! syntax: should be true")
	}
}

func TestParentheses(t *testing.T) {
	data := map[string]any{"a": 1.0, "b": 2.0, "c": 3.0}
	if !eval(t, "(a < b) and (b < c)", data) {
		t.Error("(1<2) and (2<3) should be true")
	}
	if !eval(t, "not (a > b)", data) {
		t.Error("not (1>2) should be true")
	}
}

func TestBoolLiterals(t *testing.T) {
	data := map[string]any{}
	if !eval(t, "true", data) {
		t.Error("true should be true")
	}
	if eval(t, "false", data) {
		t.Error("false should be false")
	}
}

func TestMissingField(t *testing.T) {
	data := map[string]any{}
	// Missing field returns empty string, which is falsy.
	if eval(t, `missing_field == "something"`, data) {
		t.Error("missing field should be empty string, not 'something'")
	}
}

func TestGoTemplateBackwardCompat(t *testing.T) {
	data := map[string]any{"score": 8.0}
	if !eval(t, "{{gt .score 7.0}}", data) {
		t.Error("go template backward compat: gt 8.0 7.0 should be true")
	}
	if eval(t, "{{lt .score 7.0}}", data) {
		t.Error("go template backward compat: lt 8.0 7.0 should be false")
	}
}

func TestCompileError(t *testing.T) {
	_, err := Compile("{{.bad syntax")
	if err == nil {
		t.Error("expected compile error for invalid template")
	}
}

func TestUnexpectedToken(t *testing.T) {
	_, err := Compile("score > ")
	if err == nil {
		t.Error("expected compile error for incomplete expression")
	}
}

func TestDateComparisons(t *testing.T) {
	// A time.Time value 90 days ago should be before daysago(60).
	ninetyDaysAgo := time.Now().AddDate(0, 0, -90)
	data := map[string]any{"tvdb_first_air_date": ninetyDaysAgo}
	if !eval(t, "tvdb_first_air_date < daysago(60)", data) {
		t.Error("90 days ago should be before 60 days ago")
	}

	// A time.Time value 30 days ago should NOT be before daysago(60).
	thirtyDaysAgo := time.Now().AddDate(0, 0, -30)
	data2 := map[string]any{"tvdb_first_air_date": thirtyDaysAgo}
	if eval(t, "tvdb_first_air_date < daysago(60)", data2) {
		t.Error("30 days ago should not be before 60 days ago")
	}
}

func TestNowFunction(t *testing.T) {
	if !eval(t, "now() > daysago(1)", map[string]any{}) {
		t.Error("now() should be after daysago(1)")
	}
}

func TestDateStringParsing(t *testing.T) {
	if !eval(t, `date("2020-01-01") < now()`, map[string]any{}) {
		t.Error(`date("2020-01-01") should be before now()`)
	}
}

func TestComplexExpression(t *testing.T) {
	data := map[string]any{
		"tmdb_vote_average": 7.5,
		"source":            "HD",
		"genre":             "action thriller",
	}
	expr := `tmdb_vote_average >= 7.0 and source != "CAM" and genre contains "action"`
	if !eval(t, expr, data) {
		t.Error("complex expression should be true")
	}
	expr2 := `tmdb_vote_average >= 8.0 and source != "CAM"`
	if eval(t, expr2, data) {
		t.Error("score < 8.0 so should be false")
	}
}
