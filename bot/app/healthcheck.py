import asyncio
import os
import sys
import aiohttp


async def main() -> int:
    host = os.getenv("GUARD_HOST", "127.0.0.1")
    port = os.getenv("GUARD_PORT", "8765")
    try:
        async with aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=2)) as s:
            async with s.get(f"http://{host}:{port}/health") as r:
                return 0 if r.status == 200 else 1
    except Exception:
        return 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
