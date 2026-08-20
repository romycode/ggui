package goname

import "testing"

func TestPascal(t *testing.T) {
	cases := map[string]string{
		"compositor":           "Compositor",
		"shm_pool":              "ShmPool",
		"data_device_manager":   "DataDeviceManager",
		"create_surface":        "CreateSurface",
		"":                      "",
	}
	for in, want := range cases {
		if got := Pascal(in); got != want {
			t.Errorf("Pascal(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCamel(t *testing.T) {
	cases := map[string]string{
		"callback_data": "callbackData",
		"mime_type":     "mimeType",
		"name":          "name",
		"":              "",
	}
	for in, want := range cases {
		if got := Camel(in); got != want {
			t.Errorf("Camel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStripPrefix(t *testing.T) {
	cases := []struct{ xmlName, prefix, want string }{
		{"wl_compositor", "wl_", "compositor"},
		{"wl_shm_pool", "wl_", "shm_pool"},
		{"xdg_wm_base", "xdg_", "wm_base"},
		{"wl_compositor", "xdg_", "wl_compositor"}, // prefijo que no aplica: sin cambios
	}
	for _, c := range cases {
		if got := StripPrefix(c.xmlName, c.prefix); got != c.want {
			t.Errorf("StripPrefix(%q, %q) = %q, want %q", c.xmlName, c.prefix, got, c.want)
		}
	}
}
