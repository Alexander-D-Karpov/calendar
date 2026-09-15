from app.utils import normalize_tool_name, parse_agent_response


def test_normalize_tool_name():
    assert normalize_tool_name("calendar.delete_Event") == "calendardeleteevent"


def test_parse_json():
    assert parse_agent_response('{"status":"ask","text":"Which one?","refs":[]}')["status"] == "ask"


def test_parse_plain_fallback():
    out = parse_agent_response("created")
    assert out == {"status": "done", "text": "created", "refs": []}
