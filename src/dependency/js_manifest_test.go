package dependency

import "testing"

func TestSetManifestOverride(t *testing.T) {
	cases := []struct {
		name string
		in   string
		path []string
		want string
	}{
		{
			name: "appends to an existing object, keeping order and indent",
			in:   "{\n    \"name\": \"x\",\n    \"overrides\": {\n        \"zeta\": \"^1.0.0\"\n    }\n}\n",
			path: []string{"overrides"},
			want: "{\n    \"name\": \"x\",\n    \"overrides\": {\n        \"zeta\": \"^1.0.0\",\n        \"foo\": \"^2.0.1\"\n    }\n}\n",
		},
		{
			name: "creates the object after the last root member",
			in:   "{\n  \"name\": \"x\",\n  \"private\": true\n}\n",
			path: []string{"resolutions"},
			want: "{\n  \"name\": \"x\",\n  \"private\": true,\n  \"resolutions\": {\n    \"foo\": \"^2.0.1\"\n  }\n}\n",
		},
		{
			name: "creates nested pnpm.overrides",
			in:   "{\n  \"name\": \"x\"\n}\n",
			path: []string{"pnpm", "overrides"},
			want: "{\n  \"name\": \"x\",\n  \"pnpm\": {\n    \"overrides\": {\n      \"foo\": \"^2.0.1\"\n    }\n  }\n}\n",
		},
		{
			name: "fills an empty object",
			in:   "{\n  \"overrides\": {}\n}\n",
			path: []string{"overrides"},
			want: "{\n  \"overrides\": {\n    \"foo\": \"^2.0.1\"\n  }\n}\n",
		},
		{
			name: "replaces an existing value in place",
			in:   "{\n  \"overrides\": {\n    \"foo\": \"^2.0.0\",\n    \"bar\": \"1\"\n  }\n}\n",
			path: []string{"overrides"},
			want: "{\n  \"overrides\": {\n    \"foo\": \"^2.0.1\",\n    \"bar\": \"1\"\n  }\n}\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := setManifestOverride([]byte(c.in), c.path, "foo", "^2.0.1")
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, c.want)
			}
			if v, ok := manifestOverride(got, c.path, "foo"); !ok || v != "^2.0.1" {
				t.Fatalf("read back %q %v", v, ok)
			}
		})
	}
}

func TestRemoveManifestOverride(t *testing.T) {
	cases := []struct {
		name string
		in   string
		path []string
		want string
	}{
		{
			name: "removes one of several",
			in:   "{\n  \"overrides\": {\n    \"a\": \"1\",\n    \"foo\": \"^2.0.1\",\n    \"b\": \"2\"\n  }\n}\n",
			path: []string{"overrides"},
			want: "{\n  \"overrides\": {\n    \"a\": \"1\",\n    \"b\": \"2\"\n  }\n}\n",
		},
		{
			name: "removes the first member",
			in:   "{\n  \"overrides\": {\n    \"foo\": \"^2.0.1\",\n    \"b\": \"2\"\n  }\n}\n",
			path: []string{"overrides"},
			want: "{\n  \"overrides\": {\n    \"b\": \"2\"\n  }\n}\n",
		},
		{
			name: "drops an object the removal empties",
			in:   "{\n  \"name\": \"x\",\n  \"resolutions\": {\n    \"foo\": \"^2.0.1\"\n  },\n  \"version\": \"1\"\n}\n",
			path: []string{"resolutions"},
			want: "{\n  \"name\": \"x\",\n  \"version\": \"1\"\n}\n",
		},
		{
			name: "drops emptied nested pnpm objects",
			in:   "{\n  \"name\": \"x\",\n  \"pnpm\": {\n    \"overrides\": {\n      \"foo\": \"^2.0.1\"\n    }\n  }\n}\n",
			path: []string{"pnpm", "overrides"},
			want: "{\n  \"name\": \"x\"\n}\n",
		},
		{
			name: "keeps pnpm when it holds other settings",
			in:   "{\n  \"pnpm\": {\n    \"overrides\": {\n      \"foo\": \"^2.0.1\"\n    },\n    \"onlyBuiltDependencies\": [\"esbuild\"]\n  }\n}\n",
			path: []string{"pnpm", "overrides"},
			want: "{\n  \"pnpm\": {\n    \"onlyBuiltDependencies\": [\"esbuild\"]\n  }\n}\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := removeManifestOverride([]byte(c.in), c.path, "foo")
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, c.want)
			}
		})
	}
}

func TestSetManifestOverride_NumberBeforeAppend(t *testing.T) {
	in := "{\n  \"version\": 3\n}\n"
	got, err := setManifestOverride([]byte(in), []string{"overrides"}, "foo", "1")
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"version\": 3,\n  \"overrides\": {\n    \"foo\": \"1\"\n  }\n}\n"
	if string(got) != want {
		t.Fatalf("got:\n%s", got)
	}
}
