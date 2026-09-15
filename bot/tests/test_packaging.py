from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


def test_runtime_smoke_is_not_a_docker_build_layer():
    dockerfile = (ROOT / "Dockerfile").read_text(encoding="utf-8")
    assert "RUN python -m app.runtime_smoke" not in dockerfile
    assert 'CMD ["python", "-m", "app.main"]' in dockerfile


def test_compose_uses_tmpfs_for_capabilities_and_resource_limits():
    compose = (ROOT / "compose.yaml").read_text(encoding="utf-8")
    assert "/run/calendar-codex-bot/caps:mode=0700,uid=10001,gid=10001" in compose
    assert "pids_limit:" in compose
    assert "mem_limit:" in compose
    assert "cpus:" in compose
