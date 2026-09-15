from app.utils import progress_event


class Event:
    def __init__(self, method="", payload=None):
        self.method = method
        self.payload = payload


class Payload:
    def __init__(self, **kw):
        for k, v in kw.items():
            setattr(self, k, v)


def test_reasoning_delta_is_streamed_as_thinking():
    ev = Event("item/reasoning_summary_text_delta", Payload(delta="weigh"))
    assert progress_event(ev) == ("thinking", "weigh")


def test_reasoning_text_delta_is_streamed_too():
    ev = Event("item/reasoning_text_delta", Payload(delta="ing"))
    assert progress_event(ev) == ("thinking", "ing")


def test_agent_message_delta_is_ignored():
    # The reply is a structured object, so these deltas are half-built JSON.
    ev = Event("item/agent_message_delta", Payload(delta='{"status":"do'))
    assert progress_event(ev) is None


def test_mcp_progress_message_is_a_note():
    ev = Event("item/mcp_tool_call_progress", Payload(message="fetching page 2"))
    assert progress_event(ev) == ("note", "fetching page 2")


def test_tool_call_is_reported_as_a_tool_step():
    ev = Event("item/started", Payload(tool_name="createEvent"))
    assert progress_event(ev) == ("tool", "Creating an event")


def test_every_mcp_tool_is_reported_even_when_unknown():
    ev = Event("item/started", Payload(tool_name="some_new_tool"))
    kind, text = progress_event(ev)
    assert kind == "tool"
    assert "some_new_tool" in text


def test_empty_delta_does_not_emit_an_update():
    assert progress_event(Event("item/reasoning_text_delta", Payload(delta=""))) is None


def test_unknown_event_is_ignored_safely():
    assert progress_event(Event("thread/token_usage", Payload())) is None
    assert progress_event(Event("whatever", object())) is None
    assert progress_event(object()) is None
