package license

import "testing"

func TestMatch(t *testing.T) {
	for text, want := range map[string]string{
		"   GNU GENERAL PUBLIC LICENSE\n   Version 3, 29 June 2007\n\n 13. Use with the GNU Affero General Public License.": "GPL-3.0",
		"GNU AFFERO GENERAL PUBLIC LICENSE\nVersion 3, 19 November 2007\n":                                                  "AGPL-3.0",
		"GNU GENERAL PUBLIC LICENSE\nVersion 2, June 1991\n... GNU Lesser General Public License version 3":                 "GPL-2.0",
		"Licensed under the GNU General Public License version 2, or (at your option) any later version; see version 3":     "",
		"Redistribution and use in source and binary forms ... All advertising materials mentioning ... Neither the name":   "BSD-4-Clause",
		"Redistribution and use in source and binary forms ... Neither the name of the copyright holder":                    "BSD-3-Clause",
		"MIT License\n\nPermission is hereby granted, free of charge, to any\nperson obtaining a copy":                      "MIT",
		"Most files are GPL version 2 or later. AnchorJS is available under the MIT license.":                               "",
	} {
		if got := Match(text); got != want {
			t.Errorf("Match(%.40q) = %q, want %q", text, got, want)
		}
	}
}
