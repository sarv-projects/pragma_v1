"""Module entry point so the daemon can be launched with ``python -m pragma_daemon``.

The Go lifecycle manager starts the daemon via ``python3 -m pragma_daemon
--socket <path>``; without this file the package cannot be executed and the
daemon never starts.
"""

import asyncio

from pragma_daemon.main import main

if __name__ == "__main__":
    asyncio.run(main())
