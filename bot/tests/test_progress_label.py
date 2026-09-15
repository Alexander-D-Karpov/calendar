from app.utils import progress_label


class Event:
    """Duck-typed stand-in for a Codex turn notification."""

    def __init__(self, method="", payload=None):
        self.method = method
        self.payload = payload


class Payload:
    def __init__(self, **kw):
        for k, v in kw.items():
            setattr(self, k, v)


def test_known_read_tool_gets_a_friendly_label():
    ev = Event("item/started", Payload(tool_name="listEvents"))
    assert progress_label(ev) == "Reading your calendar"


def test_known_write_tool_gets_a_friendly_label():
    assert progress_label(Event("item/started", Payload(tool_name="createEvent"))) == "Creating an event"
    assert progress_label(Event("item/started", Payload(tool_name="deleteTodo"))) == "Deleting a todo"


def test_tool_name_is_matched_regardless_of_punctuation_or_case():
    for name in ("create_event", "CreateEvent", "calendar.create-event"):
        assert progress_label(Event("item/started", Payload(tool_name=name))) == "Creating an event"


def test_tool_name_nested_on_the_item_is_found():
    ev = Event("item/started", Payload(item=Payload(name="search_bot_context")))
    assert progress_label(ev) == "Looking through earlier requests"


def test_unknown_tool_still_names_itself():
    ev = Event("item/started", Payload(tool_name="frobnicate"))
    assert progress_label(ev) == "Calling frobnicate"


def test_turn_started_reports_thinking():
    assert progress_label(Event("turn/started", Payload())) == "Thinking"


def test_command_item_reports_running():
    ev = Event("item/started", Payload(item=Payload(item_type="command_execution")))
    assert progress_label(ev) == "Running a command"


def test_unrecognised_event_produces_no_update():
    assert progress_label(Event("thread/token_usage", Payload())) is None
    assert progress_label(Event("item/started", None)) is None


def test_a_payload_without_any_expected_attribute_is_safe():
    # The SDK is pinned but moves fast; an unknown shape must not raise.
    assert progress_label(Event("something/new", object())) is None
    assert progress_label(object()) is None
