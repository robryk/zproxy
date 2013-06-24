zproxy
======

A proxy that tries to find pieces shared between otherwise unrelated files.

Usage
======

Start hasher on a high-bandwidth host. Start proxy locally, pointing it at the previously-started hasher
and at a directory to be used as a cache.
