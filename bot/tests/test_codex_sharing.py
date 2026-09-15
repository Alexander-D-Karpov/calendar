import json

import pytest

pytest.importorskip("openai_codex")

from app.codex_runtime import CodexRuntime


class Settings:
    def __init__(self, root):
        self.codex_dir = root / "codex"
        self.cap_dir = root / "caps"
        self.codex_dir.mkdir(parents=True, exist_ok=True)


def runtime(tmp_path):
    return CodexRuntime(Settings(tmp_path))


def login(rt, uid, secret="own"):
    path = rt.auth_path(uid)
    path.write_text(json.dumps({"token": secret}), encoding="utf-8")
    return path


LENDER, BORROWER = 1, 2


def test_borrower_receives_a_copy_of_the_lenders_credentials(tmp_path):
    rt = runtime(tmp_path)
    login(rt, LENDER, "lender-secret")

    assert rt.lend_auth(BORROWER, LENDER) is True
    assert json.loads(rt.auth_path(BORROWER).read_text())["token"] == "lender-secret"


def test_borrowed_credentials_are_not_mistaken_for_a_real_login(tmp_path):
    rt = runtime(tmp_path)
    login(rt, LENDER)
    rt.lend_auth(BORROWER, LENDER)

    assert rt.has_own_auth(LENDER) is True
    assert rt.has_own_auth(BORROWER) is False


def test_a_relogin_by_the_lender_propagates_on_the_next_request(tmp_path):
    rt = runtime(tmp_path)
    login(rt, LENDER, "first")
    rt.lend_auth(BORROWER, LENDER)
    login(rt, LENDER, "second")

    rt.lend_auth(BORROWER, LENDER)
    assert json.loads(rt.auth_path(BORROWER).read_text())["token"] == "second"


def test_a_users_own_login_is_never_overwritten_by_a_grant(tmp_path):
    rt = runtime(tmp_path)
    login(rt, LENDER, "lender-secret")
    login(rt, BORROWER, "mine")

    assert rt.lend_auth(BORROWER, LENDER) is True
    assert json.loads(rt.auth_path(BORROWER).read_text())["token"] == "mine"


def test_revoking_removes_only_a_borrowed_copy(tmp_path):
    rt = runtime(tmp_path)
    login(rt, LENDER)
    rt.lend_auth(BORROWER, LENDER)

    rt.drop_lent_auth(BORROWER)
    assert not rt.auth_path(BORROWER).exists()
    assert rt.auth_path(LENDER).exists(), "the lender's own credentials must survive"


def test_revoking_never_deletes_a_users_own_login(tmp_path):
    rt = runtime(tmp_path)
    login(rt, BORROWER, "mine")

    rt.drop_lent_auth(BORROWER)
    assert rt.auth_path(BORROWER).exists()
    assert json.loads(rt.auth_path(BORROWER).read_text())["token"] == "mine"


def test_lending_fails_when_the_lender_has_not_logged_in(tmp_path):
    rt = runtime(tmp_path)
    assert rt.lend_auth(BORROWER, LENDER) is False
    assert not rt.auth_path(BORROWER).exists()


def test_borrowed_file_is_owner_only(tmp_path):
    rt = runtime(tmp_path)
    login(rt, LENDER)
    rt.lend_auth(BORROWER, LENDER)
    assert rt.auth_path(BORROWER).stat().st_mode & 0o077 == 0
