from datetime import timezone
from zoneinfo import ZoneInfo

from app.utils import local_time_label

# Fixed UTC+3 with no DST, so the expectations never depend on the date.
PLUS3 = ZoneInfo("Europe/Istanbul")


def test_utc_timestamp_is_shown_in_the_readers_timezone():
    assert local_time_label("2030-01-02T05:00:00Z", PLUS3) == "08:00"


def test_offset_timestamp_is_not_shifted_twice():
    assert local_time_label("2030-01-02T09:00:00+03:00", PLUS3) == "09:00"


def test_mixed_offsets_in_one_agenda_become_comparable():
    # The reported failure: the same instant written two ways read as two
    # different times when the raw value is printed.
    raw = ["2030-01-02T05:00:00Z", "2030-01-02T09:00:00+03:00"]
    assert [local_time_label(x, PLUS3) for x in raw] == ["08:00", "09:00"]


def test_date_only_value_is_not_reported_as_midnight():
    assert local_time_label("2030-01-02", PLUS3) == "all day"


def test_bare_clock_time_is_passed_through():
    assert local_time_label("14:00", PLUS3) == "14:00"


def test_naive_timestamp_keeps_its_own_clock():
    assert local_time_label("2030-01-02T08:30:00", PLUS3) == "08:30"


def test_unparseable_value_is_returned_rather_than_dropped():
    assert local_time_label("sometime tuesday", PLUS3) == "sometime tuesday"


def test_missing_value_is_empty():
    assert local_time_label(None, PLUS3) == ""
    assert local_time_label("", PLUS3) == ""


def test_utc_reader_sees_utc():
    assert local_time_label("2030-01-02T05:00:00Z", timezone.utc) == "05:00"
