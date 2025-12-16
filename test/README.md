Test environment
---------------

This folder contains a minimal test harness to run a Matrix Synapse homeserver for integration tests.

Usage
-----

- Start the test stack (run Synapse and create test users from `test.env`):

```bash
cd test
./test.sh start
```

- Run the tests (this will start the stack if not already running):

```bash
cd test
./test.sh run
```

- Stop and remove the test stack:

```bash
cd test
./test.sh stop
```


Notes
-----
- `test.sh` uses `podman run` to start the `matrixdotorg/synapse:latest` container on port `8008`.
- `test.sh` expects `test/test.env` to provide `MATRIX_AS_TOKEN`, `USER1`, `USER1_PASSWORD`, `USER2`, `USER2_PASSWORD`.
 - `test/homeserver.yaml` has been simplified and no longer contains OIDC/Dex configuration; it is standalone for local testing.
