from app.utils import split_message, telegram_html


def test_bold_becomes_html():
    assert telegram_html("Task: **Buy milk**.") == "Task: <b>Buy milk</b>."


def test_link_becomes_anchor():
    got = telegram_html("[Standup](https://meet.example.com/room)")
    assert got == '<a href="https://meet.example.com/room">Standup</a>'


def test_italic_becomes_html():
    assert telegram_html("this is *important*") == "this is <i>important</i>"


def test_underscores_inside_a_word_are_left_alone():
    assert telegram_html("search_bot_context is a tool") == "search_bot_context is a tool"


def test_html_in_model_output_is_escaped_not_executed():
    assert telegram_html("a < b & c > d") == "a &lt; b &amp; c &gt; d"
    assert telegram_html("<script>alert(1)</script>") == "&lt;script&gt;alert(1)&lt;/script&gt;"


def test_inline_code_is_preserved_verbatim():
    assert telegram_html("run `a < b` now") == "run <code>a &lt; b</code> now"


def test_markdown_inside_code_is_not_converted():
    assert telegram_html("`**not bold**`") == "<code>**not bold**</code>"


def test_fenced_block_becomes_pre():
    assert telegram_html("```\nx = 1 < 2\n```") == "<pre><code>x = 1 &lt; 2</code></pre>"


def test_heading_becomes_bold():
    assert telegram_html("## Today") == "<b>Today</b>"


def test_non_ascii_text_survives_unchanged():
    src = "Встреча в 10:00"
    assert telegram_html(src) == src


def test_bullet_lines_are_untouched():
    src = "• 09:00–10:30 — Team sync, remote."
    assert telegram_html(src) == src


def test_short_text_is_one_message():
    assert split_message("hi") == ["hi"]
    assert split_message("") == []


def test_long_text_splits_on_line_boundaries_without_losing_the_tail():
    body = "\n".join(f"line {i}" for i in range(1, 2001))
    parts = split_message(body, limit=200)
    assert len(parts) > 1
    assert all(len(p) <= 200 for p in parts)
    # nothing is dropped, which the old 4096 truncation did silently
    assert "line 2000" in parts[-1]
    assert "\n".join(parts).replace("\n", "") == body.replace("\n", "")


def test_split_falls_back_when_a_single_line_exceeds_the_limit():
    parts = split_message("x" * 500, limit=100)
    assert len(parts) == 5
    assert "".join(parts) == "x" * 500
