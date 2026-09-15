from app.guard import ALLOWED_DELETE_NAMES, SEARCH_CONTEXT_TOOL, GuardServer


def _tool(name, **annotations):
    return {"name": name, "annotations": dict(annotations)} if annotations else {"name": name}


def test_allowed_delete_is_not_advertised_as_destructive():
    # Codex escalates destructive tools and the bot denies all escalations, so
    # a delete advertised as destructive can never reach the guard's preflight.
    out = GuardServer._expose(_tool("deleteEvent", destructiveHint=True, idempotentHint=True))
    assert out["annotations"]["destructiveHint"] is False
    assert out["annotations"]["idempotentHint"] is True


def test_every_allowed_delete_name_is_rewritten():
    for name in ALLOWED_DELETE_NAMES:
        out = GuardServer._expose(_tool(name, destructiveHint=True))
        assert out["annotations"]["destructiveHint"] is False, name


def test_delete_without_annotations_gets_an_explicit_false():
    # Absent destructiveHint defaults to true, so dropping it is not enough.
    out = GuardServer._expose(_tool("deleteTodo"))
    assert out["annotations"]["destructiveHint"] is False


def test_non_delete_tools_are_passed_through_untouched():
    src = _tool("createEvent")
    assert GuardServer._expose(src) == src
    read = _tool("listEvents", readOnlyHint=True, idempotentHint=True)
    assert GuardServer._expose(read) == read


def test_rewrite_does_not_mutate_the_original_tool():
    src = _tool("deleteEvent", destructiveHint=True)
    GuardServer._expose(src)
    assert src["annotations"]["destructiveHint"] is True


def test_context_search_is_advertised_read_only():
    ann = SEARCH_CONTEXT_TOOL["annotations"]
    assert ann["readOnlyHint"] is True
    assert ann["destructiveHint"] is False
