# Fixtures of internal/scan/rules/drydock/python.yaml: `ruleid:` marks a line the rule must find, `ok:` one it must not.
import hashlib
import os
import pickle
import subprocess
import tempfile

import requests
import yaml


def run(name, data, cur, app):
    # ruleid: drydock.py.subprocess-shell-true
    subprocess.run("git log " + name, shell=True)
    # ok: drydock.py.subprocess-shell-true
    subprocess.run(["git", "log", name])
    # ok: drydock.py.subprocess-shell-true
    subprocess.run("git status", shell=True)

    # ruleid: drydock.py.os-system-of-non-literal
    os.system("rm " + name)
    # ok: drydock.py.os-system-of-non-literal
    os.system("clear")

    # ruleid: drydock.py.eval-of-non-literal
    eval(data)
    # ok: drydock.py.eval-of-non-literal
    eval("1 + 1")

    # ruleid: drydock.py.pickle-load
    obj = pickle.loads(data)

    # ruleid: drydock.py.yaml-load-unsafe
    cfg = yaml.load(data)
    # ok: drydock.py.yaml-load-unsafe
    cfg = yaml.load(data, Loader=yaml.SafeLoader)
    # ok: drydock.py.yaml-load-unsafe
    cfg = yaml.safe_load(data)

    # ruleid: drydock.py.tls-certificate-check-off
    requests.get("https://example.com", verify=False)
    # ok: drydock.py.tls-certificate-check-off
    requests.get("https://example.com", verify="/etc/ca.pem")

    # ruleid: drydock.py.sql-built-from-data
    cur.execute("SELECT * FROM users WHERE name = '%s'" % name)
    # ruleid: drydock.py.sql-built-from-data
    cur.execute(f"SELECT * FROM users WHERE name = '{name}'")
    # ok: drydock.py.sql-built-from-data
    cur.execute("SELECT * FROM users WHERE name = %s", (name,))

    # ruleid: drydock.py.weak-hash
    hashlib.md5(data).hexdigest()
    # ok: drydock.py.weak-hash
    hashlib.md5(data, usedforsecurity=False).hexdigest()
    # ok: drydock.py.weak-hash
    hashlib.sha256(data).hexdigest()

    # ruleid: drydock.py.flask-debug-on
    app.run(host="0.0.0.0", debug=True)
    # ok: drydock.py.flask-debug-on
    app.run(host="127.0.0.1")

    # ruleid: drydock.py.tempfile-mktemp
    path = tempfile.mktemp()
    # ok: drydock.py.tempfile-mktemp
    fd, path = tempfile.mkstemp()
