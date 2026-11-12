package rule

import "testing"

func TestMatch(t *testing.T) {
	lib := NewLibrary([]Rule{
		{ID: 1, Pattern: `(?i)什么时候.{0,4}到岗|入职时间`, Answer: "收到 offer 后两周内可到岗。"},
		{ID: 2, Pattern: `薪资|期望薪水|多少钱`, Answer: "薪资可按贵司岗位范围沟通。"},
	})
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"请问您大概什么时候可以到岗？", 1, true},
		{"expected salary? 期望薪资多少", 2, true},
		{"你好，我们看了您的简历", 0, false},
	}
	for _, c := range cases {
		got, ok := lib.Match(c.in)
		if ok != c.ok || (ok && got.ID != c.want) {
			t.Errorf("Match(%q) = (%d, %v), want (%d, %v)", c.in, got.ID, ok, c.want, c.ok)
		}
	}
}

func TestFirstMatchWins(t *testing.T) {
	lib := NewLibrary([]Rule{
		{ID: 1, Pattern: `到岗`, Answer: "a"},
		{ID: 2, Pattern: `到岗时间`, Answer: "b"},
	})
	if r, _ := lib.Match("到岗时间何时"); r.ID != 1 {
		t.Fatalf("expect first rule to win, got %d", r.ID)
	}
}

func TestReloadSkipsInvalid(t *testing.T) {
	lib := NewLibrary([]Rule{
		{ID: 1, Pattern: `(`, Answer: "bad"},
		{ID: 2, Pattern: `hi`, Answer: "ok"},
	})
	if _, ok := lib.Match("hi"); !ok {
		t.Fatal("valid rule should still match")
	}
}

func TestValidate(t *testing.T) {
	if Validate(`(`, "a") {
		t.Error("invalid regex should fail validation")
	}
	if !Validate(`到岗`, "b") {
		t.Error("valid rule should pass validation")
	}
	if Validate(`到岗`, " ") {
		t.Error("empty answer should fail validation")
	}
}
