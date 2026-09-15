from datetime import timezone
from zoneinfo import ZoneInfo

from app.utils import local_time_label

VILNIUS = ZoneInfo("Europe/Vilnius")  # UTC+3 in September


def test_utc_timestamp_is_shown_in_the_readers_timezone():
    assert local_time_label("2026-09-15T06:50:00Z", VILNIUS) == "09:50"


def test_offset_timestamp_is_not_shifted_twice():
    assert local_time_label("2026-09-15T10:00:00+03:00", VILNIUS) == "10:00"


def test_mixed_offsets_in_one_agenda_become_comparable():
    # The real failure: these two are 10 minutes apart but read as 06:50 vs 10:00.
    raw = ["2026-09-15T06:50:00Z", "2026-09-15T10:00:00+03:00"]
    assert [local_time_label(x, VILNIUS) for x in raw] == ["09:50", "10:00"]


def test_date_only_value_is_not_reported_as_midnight():
    assert local_time_label("2026-09-15", VILNIUS) == "all day"


def test_bare_clock_time_is_passed_through():
    assert local_time_label("14:00", VILNIUS) == "14:00"


def test_naive_timestamp_keeps_its_own_clock():
    assert local_time_label("2026-09-15T08:30:00", VILNIUS) == "08:30"


def test_unparseable_value_is_returned_rather_than_dropped():
    assert local_time_label("sometime tuesday", VILNIUS) == "sometime tuesday"


def test_missing_value_is_empty():
    assert local_time_label(None, VILNIUS) == ""
    assert local_time_label("", VILNIUS) == ""


def test_utc_reader_sees_utc():
    assert local_time_label("2026-09-15T06:50:00Z", timezone.utc) == "06:50"
