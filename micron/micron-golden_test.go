// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Fixture inputs for the Go micron port's live cross-checks against Python's
// RNS.Utilities.rngit convert_markdown_to_micron and highlight_code. The
// expected output for each fixture is captured fresh from the installed Python
// at test time (see python-parity_test.go); these tables hold only the inputs,
// not committed "golden" output snapshots.
package micron

// goldenConvert holds markdown->micron inputs run through both Go
// ConvertMarkdownToMicron/Converter.FormatBlock and Python
// convert_markdown_to_micron with default settings (no syntax highlighter).
// The Go port must match Python byte for byte.
var goldenConvert = []struct{ name, input string }{
	{"h1", "# Hello World"},
	{"h2", "## Section Two"},
	{"h3", "### Subsection"},
	{"h4", "#### H4"},
	{"h5", "##### H5"},
	{"h6", "###### H6"},
	{"h7_clamped", "####### H7 (clamps to 6)"},
	{"paragraph", "Just a plain paragraph of text here."},
	{"two_paragraphs", "First paragraph.\n\nSecond paragraph."},
	{"unordered_dash", "- item one\n- item two\n- item three"},
	{"unordered_star", "* star one\n* star two"},
	{"unordered_plus", "+ plus one\n+ plus two"},
	{"nested_list", "- top\n  - nested\n    - deeper\n- back"},
	{"ordered_text", "1. first\n2. second\n3. third"},
	{"inline_code", "Use `fmt.Println` to print."},
	{"inline_code_two", "Call `foo()` and `bar()` together."},
	{"bold", "This is **bold** text."},
	{"bold_underscore", "This is __bold__ text."},
	{"italic", "This is *italic* text."},
	{"italic_underscore", "This is _italic_ text."},
	{"bold_italic", "**bold** and *italic* together."},
	{"link", "See [the docs](docs/index) for more."},
	{"link_external", "Visit [example](https://example.com) now."},
	{"link_anchor", "Go to [section](page#section) here."},
	{"link_two", "[a](urla) and [b](urlb)."},
	{"image_basic", "![alt text](image.png)"},
	{"blockquote", "> This is a quote."},
	{"blockquote_multi", "> line one\n> line two\n> line three"},
	{"blockquote_para", "> A quoted paragraph that is long enough to wrap onto multiple lines within the max width."},
	{"hr_dashes", "---"},
	{"hr_stars", "***"},
	{"hr_underscores", "___"},
	{"hr_equals", "==="},
	{"hr_indented", "   ---"},
	{"code_block_plain", "```\nplain code\nline two\n```"},
	{"code_block_lang", "```go\npackage main\n```"},
	{"code_block_rawmu", "```rawmu\n`cShould stay raw\n```"},
	{"code_block_unclosed", "```\nunclosed block"},
	{"table_simple", "| Name | Age |\n| --- | --- |\n| Alice | 30 |\n| Bob | 25 |"},
	{"table_aligned", "| Left | Center | Right |\n| :--- | :---: | ---: |\n| a | b | c |"},
	{"table_no_pipes_edges", "Name | Age\n--- | ---\nAlice | 30"},
	{"mixed", "# Title\n\nA paragraph with **bold** and `code`.\n\n- list item\n\n> quote\n\n---\n\n| H1 | H2 |\n| -- | -- |\n| x | y |"},
	{"leading_dash_text", "-not a list"},
	{"leading_lt", "<not a tag>"},
	{"escape_backslash", "A back\\\\slash in text."},
	{"trailing_blank", "para\n\n"},
	{"empty", ""},
	{"only_newline", "\n"},
	{"link_then_code", "[link](url) and `code` after."},
	{"code_then_link", "`code` and [link](url) after."},
	{"header_with_link", "# Title with [link](dest)"},
	{"list_with_inline", "- item with **bold** and `code`"},
	{"table_long_cell", "| short | a very very very very very very long cell |\n| --- | --- |\n| x | y |"},
	{"paragraph_wrapping", "This is a long paragraph that should wrap onto multiple lines because it exceeds the default max width of one hundred characters when rendered by the micron format block converter."},
}

// goldenPlain holds highlight_code inputs with no language and no filename, so
// both Go and Python take the plain-text fallback path. The expected output is
// captured live from Python highlight_code(content, None, None).
var goldenPlain = []struct{ name, content string }{
	{"plain_no_lang", "just text\n"},
	{"plain_empty", ""},
	{"plain_multiline", "line one\nline two\nline three\n"},
	{"plain_backtick", "a `b` c\n"},
	{"plain_backslash", "a \\\\ b\n"},
	{"plain_no_trailing_nl", "no newline"},
	{"plain_trailing_nl", "with newline\n"},
}

// goldenColoredRef holds inputs for the coloured-reference structural check.
// The Pygments reference output is captured live from Python
// highlight_code(content, None, language); the test asserts every colour code
// in the reference also appears in the Go hand-tokenised output (structural,
// not byte-for-byte, since hand tokenisation differs from Pygments).
var goldenColoredRef = []struct{ name, content, language string }{
	{"py_comment", "# a comment\n", "python"},
	{"py_string", "x = \"hello\"\n", "python"},
	{"py_func", "def foo(x):\n    return x\n", "python"},
	{"go_func", "func foo(x int) int {\n    return x\n}\n", "go"},
	{"go_string", "s := \"hello\"\n", "go"},
	{"bash_comment", "# comment\n", "bash"},
	{"bash_export", "export FOO=bar\n", "bash"},
	{"plain_no_lang", "just text\n", ""},
	{"plain_empty", "", ""},
	{"py_keywords", "if True:\n    pass\n", "python"},
	{"env_alias", "echo hi\n", "env"},
}
