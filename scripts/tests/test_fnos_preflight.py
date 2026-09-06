import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).resolve().parents[1] / "fnos_preflight.py"
spec = importlib.util.spec_from_file_location("fnos_preflight", SCRIPT)
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)


class PreflightTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.usb = self.root / "bus/usb/devices"
        self.usb.mkdir(parents=True)

    def collect(self, **kwargs):
        return probe.collect(sys_root=self.root, system="Linux", machine="aarch64",
                             probe=lambda _: False, **kwargs)

    def statuses(self, report):
        return {item["id"]: item["status"] for item in report["checks"]}

    def device(self, vid="2ca3", pid="4006"):
        device = self.usb / "1-2"
        device.mkdir()
        (device / "idVendor").write_text(vid)
        (device / "idProduct").write_text(pid)
        # Must never appear in a report, even on an allowlisted device.
        (device / "serial").write_text("SECRET_SIM_123")
        (device / "product").write_text("SECRET_PHONE_NUMBER")
        return device

    def test_known_device_is_only_candidate(self):
        self.device()
        report = self.collect()
        self.assertEqual(report["devices"][0]["usb_id"], "2ca3:4006")
        self.assertEqual(self.statuses(report)["usb"], "pass")
        self.assertEqual(self.statuses(report)["two_way_audio"], "not_tested")

    def test_no_private_identifiers(self):
        self.device()
        raw = json.dumps(self.collect())
        self.assertNotIn("SECRET", raw)
        self.assertNotIn(str(self.root), raw)

    def test_unknown_device_is_not_guessed(self):
        self.device("1234", "5678")
        self.assertEqual(self.collect()["devices"], [])

    def test_rewritten_vid_is_not_exact_model_claim(self):
        self.device("2c7c", "0125")
        self.assertIn("unverified", self.collect()["devices"][0]["family"])

    def test_armv7_is_not_claimed_supported(self):
        result = probe.collect(sys_root=self.root, system="Linux", machine="armv7l", probe=lambda _: False)
        self.assertEqual(self.statuses(result)["architecture"], "fail")

    def test_windows_does_not_probe_docker_or_linux(self):
        def forbidden(_):
            self.fail("must not execute Linux probes on Windows")
        result = probe.collect(sys_root=self.root, system="Windows", machine="AMD64", probe=forbidden)
        self.assertEqual(self.statuses(result)["linux"], "fail")
        self.assertEqual(result["devices"], [])

    def test_absent_sysfs_does_not_crash(self):
        self.assertEqual(probe.usb_inventory(self.root / "absent"), ([], False))

    def test_usb_does_not_imply_driver_support(self):
        self.device()
        status = self.statuses(self.collect())
        for name in ["serial", "qmi", "audio"]:
            self.assertEqual(status[name], "not_tested")

    def test_all_real_operations_are_never_marked_verified(self):
        self.device()
        status = self.statuses(self.collect())
        for name in ["sms_receive", "sms_send", "call_in", "call_out", "two_way_audio", "recording"]:
            self.assertEqual(status[name], "not_tested")

    def test_storage_is_not_created(self):
        missing = self.root / "missing"
        result = probe.storage_check("data", str(missing), 10001, 10001)
        self.assertEqual(result["status"], "fail")
        self.assertFalse(missing.exists())

    def test_relative_storage_rejected(self):
        self.assertEqual(probe.storage_check("data", "relative", 10001, 10001)["status"], "fail")

    def test_unspecified_storage_unverified(self):
        self.assertEqual(probe.storage_check("data", None, 10001, 10001)["status"], "not_tested")

    def test_same_storage_rejected(self):
        result = self.collect(data_dir=str(self.root), recordings_dir=str(self.root))
        self.assertEqual(self.statuses(result)["storage_separation"], "fail")

    def test_docker_uses_explicit_local_socket(self):
        calls = []
        with patch.object(probe.shutil, "which", return_value="/usr/bin/docker"):
            result = probe.collect(sys_root=self.root, system="Linux", machine="x86_64",
                                   probe=lambda args: calls.append(args) or True)
        self.assertEqual(calls, [["/usr/bin/docker", "--host", "unix:///var/run/docker.sock", "info"], ["/usr/bin/docker", "compose", "version"]])
        self.assertEqual(self.statuses(result)["docker"], "pass")

    def test_subprocess_timeout_is_unverified(self):
        with patch.object(probe.subprocess, "run", side_effect=subprocess.TimeoutExpired("docker", 5)):
            self.assertFalse(probe.bounded_probe(["docker", "info"]))

    def test_subprocess_does_not_use_shell(self):
        with patch.object(probe.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)) as run:
            self.assertTrue(probe.bounded_probe(["docker", "info"]))
        self.assertFalse(run.call_args.kwargs["shell"])
        self.assertEqual(run.call_args.kwargs["timeout"], 5)

    def test_diagnostics_have_no_filesystem_side_effects(self):
        self.device()
        before = {str(p.relative_to(self.root)): p.read_bytes() for p in self.root.rglob("*") if p.is_file()}
        self.collect()
        after = {str(p.relative_to(self.root)): p.read_bytes() for p in self.root.rglob("*") if p.is_file()}
        self.assertEqual(before, after)

    def test_cli_output_is_utf8_json(self):
        result = subprocess.run([os.sys.executable, str(SCRIPT)], capture_output=True, check=False)
        report = json.loads(result.stdout.decode("utf-8"))
        self.assertEqual(report["schema"], "nas-comm-preflight/v1")
        self.assertIn("宿主机", report["checks"][0]["title"])


if __name__ == "__main__":
    unittest.main()
