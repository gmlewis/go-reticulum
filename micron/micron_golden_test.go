// Code generated from Python golden output. DO NOT EDIT.
//
// goldenConvert holds markdown->micron fixtures captured from
// Python convert_markdown_to_micron with default settings (no
// syntax highlighter). The Go port must match these byte for
// byte.
package micron

var goldenConvert = []struct{ name, input, want string }{
	{"h1", "# Hello World", ">Hello World"},
	{"h2", "## Section Two", ">>Section Two"},
	{"h3", "### Subsection", ">>>Subsection"},
	{"h4", "#### H4", ">>>>H4"},
	{"h5", "##### H5", ">>>>>H5"},
	{"h6", "###### H6", ">>>>>>H6"},
	{"h7_clamped", "####### H7 (clamps to 6)", "####### H7 (clamps to 6)"},
	{"paragraph", "Just a plain paragraph of text here.", "Just a plain paragraph of text here."},
	{"two_paragraphs", "First paragraph.\n\nSecond paragraph.", "First paragraph.\n\nSecond paragraph."},
	{"unordered_dash", "- item one\n- item two\n- item three", " • item one\n • item two\n • item three"},
	{"unordered_star", "* star one\n* star two", " • star one\n • star two"},
	{"unordered_plus", "+ plus one\n+ plus two", " • plus one\n • plus two"},
	{"nested_list", "- top\n  - nested\n    - deeper\n- back", " • top\n   • nested\n     • deeper\n • back"},
	{"ordered_text", "1. first\n2. second\n3. third", "1. first\n2. second\n3. third"},
	{"inline_code", "Use `fmt.Println` to print.", "Use `BT383838`Fdddfmt.Println`f`b to print."},
	{"inline_code_two", "Call `foo()` and `bar()` together.", "Call `BT383838`Fdddfoo()`f`b and `BT383838`Fdddbar()`f`b together."},
	{"bold", "This is **bold** text.", "This is `!bold`! text."},
	{"bold_underscore", "This is __bold__ text.", "This is `!bold`! text."},
	{"italic", "This is *italic* text.", "This is `*italic`* text."},
	{"italic_underscore", "This is _italic_ text.", "This is `*italic`* text."},
	{"bold_italic", "**bold** and *italic* together.", "`!bold`! and `*italic`* together."},
	{"link", "See [the docs](docs/index) for more.", "See `_`!`[the docs`:/page/docs/index]`!`_ for more."},
	{"link_external", "Visit [example](https://example.com) now.", "Visit `_`!`[example`https://example.com]`!`_ now."},
	{"link_anchor", "Go to [section](page#section) here.", "Go to `_`!`[section`:/page/page|anchor=section]`!`_ here."},
	{"link_two", "[a](urla) and [b](urlb).", "`_`!`[a`:/page/urla]`!`_ and `_`!`[b`:/page/urlb]`!`_."},
	{"image_basic", "![alt text](image.png)", "!`_`!`[alt text`:/page/image.png]`!`_"},
	{"blockquote", "> This is a quote.", " │ This is a quote."},
	{"blockquote_multi", "> line one\n> line two\n> line three", " │ line one line two line three"},
	{"blockquote_para", "> A quoted paragraph that is long enough to wrap onto multiple lines within the max width.", " │ A quoted paragraph that is long enough to wrap onto multiple lines within the max width."},
	{"hr_dashes", "---", "-"},
	{"hr_stars", "***", "-"},
	{"hr_underscores", "___", "-"},
	{"hr_equals", "===", "-"},
	{"hr_indented", "   ---", "-"},
	{"code_block_plain", "```\nplain code\nline two\n```", "`BT282828`Fddd\n`=\nplain code\nline two\n`=\n`f`b"},
	{"code_block_lang", "```go\npackage main\n```", "`BT282828`Fddd\n`=\npackage main\n`=\n`f`b"},
	{"code_block_rawmu", "```rawmu\n`cShould stay raw\n```", "`BT282828`Fddd\n`=\n\\`cShould stay raw\n`=\n`f`b"},
	{"code_block_unclosed", "```\nunclosed block", "`BT282828`Fddd\n`=\nunclosed block\n`=\n`f`b"},
	{"table_simple", "| Name | Age |\n| --- | --- |\n| Alice | 30 |\n| Bob | 25 |", "`c\n┌───────┬─────┐\n│ Name  │ Age │\n├───────┼─────┤\n│ Alice │ 30  │\n│ Bob   │ 25  │\n└───────┴─────┘\n`a"},
	{"table_aligned", "| Left | Center | Right |\n| :--- | :---: | ---: |\n| a | b | c |", "`c\n┌──────┬────────┬───────┐\n│ Left │ Center │ Right │\n├──────┼────────┼───────┤\n│ a    │   b    │     c │\n└──────┴────────┴───────┘\n`a"},
	{"table_no_pipes_edges", "Name | Age\n--- | ---\nAlice | 30", "Name | Age\n--- | ---\nAlice | 30"},
	{"mixed", "# Title\n\nA paragraph with **bold** and `code`.\n\n- list item\n\n> quote\n\n---\n\n| H1 | H2 |\n| -- | -- |\n| x | y |", ">Title\n\nA paragraph with `!bold`! and `BT383838`Fdddcode`f`b.\n\n • list item\n\n │ quote\n\n-\n\n`c\n┌─────┬─────┐\n│ H1  │ H2  │\n├─────┼─────┤\n│ x   │ y   │\n└─────┴─────┘\n`a"},
	{"leading_dash_text", "-not a list", "\\-not a list"},
	{"leading_lt", "<not a tag>", "\\<not a tag>"},
	{"escape_backslash", "A back\\\\slash in text.", "A back\\\\\\\\slash in text."},
	{"trailing_blank", "para\n\n", "para\n\n"},
	{"empty", "", ""},
	{"only_newline", "\n", "\n"},
	{"link_then_code", "[link](url) and `code` after.", "`_`!`[link`:/page/url]`!`_ and `BT383838`Fdddcode`f`b after."},
	{"code_then_link", "`code` and [link](url) after.", "`BT383838`Fdddcode`f`b and `_`!`[link`:/page/url]`!`_ after."},
	{"header_with_link", "# Title with [link](dest)", ">Title with `_`!`[link`:/page/dest]`!`_"},
	{"list_with_inline", "- item with **bold** and `code`", " • item with `!bold`! and `BT383838`Fdddcode`f`b"},
	{"table_long_cell", "| short | a very very very very very very long cell |\n| --- | --- |\n| x | y |", "`c\n┌───────┬───────────────────────────────────────────┐\n│ short │ a very very very very very very long cell │\n├───────┼───────────────────────────────────────────┤\n│ x     │ y                                         │\n└───────┴───────────────────────────────────────────┘\n`a"},
	{"paragraph_wrapping", "This is a long paragraph that should wrap onto multiple lines because it exceeds the default max width of one hundred characters when rendered by the micron format block converter.", "This is a long paragraph that should wrap onto multiple lines because it exceeds the default max width of one hundred characters when rendered by the micron format block converter."},
}

// goldenPlain holds highlight_code fixtures captured from Python
// with Pygments unavailable (the _plain_text fallback). These
// cases have no language and no filename so the Go port also
// takes its plain fallback path; output must match byte for
// byte.
var goldenPlain = []struct{ name, content, want string }{
	{"plain_no_lang", "just text\n", "`=\njust text\n\n`="},
	{"plain_empty", "", "`=\n\n`="},
	{"plain_multiline", "line one\nline two\nline three\n", "`=\nline one\nline two\nline three\n\n`="},
	{"plain_backtick", "a `b` c\n", "`=\na \\\\`b\\\\` c\n\n`="},
	{"plain_backslash", "a \\\\ b\n", "`=\na \\\\\\\\ b\n\n`="},
	{"plain_no_trailing_nl", "no newline", "`=\nno newline\n`="},
	{"plain_trailing_nl", "with newline\n", "`=\nwith newline\n\n`="},
}

// goldenColoredRef holds Pygments-produced reference output for
// languages the Go port hand-tokenises. These are NOT asserted
// byte for byte (hand tokenisation differs from Pygments);
// they drive structural colour assertions in highlight_test.go.
var goldenColoredRef = []struct{ name, content, language, ref string }{
	{"py_comment", "# a comment\n", "python", "`FT8b949e# a comment`f\n"},
	{"py_string", "x = \"hello\"\n", "python", "`FTe6edf3x`f `FTff7b72=`f `FTa5d6ff\"`f`FTa5d6ffhello`f`FTa5d6ff\"`f\n"},
	{"py_func", "def foo(x):\n    return x\n", "python", "`FTff7b72def`f `FTd2a8fffoo`f`FTb4b4b4(`f`FTe6edf3x`f`FTb4b4b4)`f`FTb4b4b4:`f\n    `FTff7b72return`f `FTe6edf3x`f\n"},
	{"go_func", "func foo(x int) int {\n    return x\n}\n", "go", "`FTff7b72func`f `FTe6edf3foo`f`FTb4b4b4(`f`FTe6edf3x`f `FTffa657int`f`FTb4b4b4)`f `FTffa657int`f `FTb4b4b4{`f\n    `FTff7b72return`f `FTe6edf3x`f\n`FTb4b4b4}`f\n"},
	{"go_string", "s := \"hello\"\n", "go", "`FTe6edf3s`f `FTff7b72:=`f `FTa5d6ff\"hello\"`f\n"},
	{"bash_comment", "# comment\n", "bash", "`FT8b949e# comment`f\n"},
	{"bash_export", "export FOO=bar\n", "bash", "`FTffa657export`f `FTe6edf3FOO`f`FTff7b72=`fbar\n"},
	{"plain_no_lang", "just text\n", "", "`=\njust text\n\n`="},
	{"plain_empty", "", "", "`=\n\n`="},
	{"py_keywords", "if True:\n    pass\n", "python", "`FTff7b72if`f `FTff7b72True`f`FTb4b4b4:`f\n    `FTff7b72pass`f\n"},
	{"env_alias", "echo hi\n", "env", "`FTffa657echo`f hi\n"},
}
