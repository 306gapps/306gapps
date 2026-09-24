#!/usr/bin/env python3
"""Write a miniature assets repo for the installer and CLI tests.

Payloads are random bytes of realistic size, so the tests exercise digest
verification and the space check without downloading anything.
"""

import hashlib
import json
import os
import random
import sys

PACKAGES = [
    # id, name, category, path, asset, KiB, extra
    ("gmscore", "Google Play services", "core",
     "product/priv-app/PrebuiltGmsCore/PrebuiltGmsCore.apk", "gmscore.apk", 320,
     {"required": True, "props": {"ro.com.google.gmsversion": "16_202509"}}),
    ("vending", "Google Play Store", "core",
     "product/priv-app/Phonesky/Phonesky.apk", "vending.apk", 90,
     {"default": True, "requires": ["gmscore"]}),
    ("gsf", "Google Services Framework", "core",
     "product/priv-app/GoogleServicesFramework/GoogleServicesFramework.apk", "gsf.apk", 12,
     {"required": True}),
    ("setupwizard", "Google Setup Wizard", "setup",
     "product/priv-app/SetupWizardPrebuilt/SetupWizardPrebuilt.apk", "setupwizard.apk", 40,
     {"default": True, "requires": ["gmscore", "vending"]}),
    ("gsa", "Google Search / Assistant", "apps",
     "product/priv-app/Velvet/Velvet.apk", "gsa.apk", 280,
     {"requires": ["gmscore"], "removes": ["product/app/QuickSearchBox"]}),
    ("dialer-google", "Google Phone", "apps",
     "system_ext/priv-app/GoogleDialer/GoogleDialer.apk", "dialer-g.apk", 60,
     {"requires": ["gmscore"], "conflicts": ["dialer-aosp"],
      "removes": ["system_ext/priv-app/Dialer"]}),
    ("dialer-aosp", "AOSP Dialer (keep stock)", "apps",
     "system_ext/priv-app/Dialer/Dialer.apk", "dialer-a.apk", 8,
     {"conflicts": ["dialer-google"]}),
    ("photos", "Google Photos", "apps",
     "product/app/Photos/Photos.apk", "photos.apk", 120, {"requires": ["gmscore"]}),
    ("gboard", "Gboard", "apps",
     "product/app/LatinIMEGooglePrebuilt/LatinIMEGooglePrebuilt.apk", "gboard.apk", 70,
     {"requires": ["gmscore"]}),
    ("pixellauncher", "Pixel Launcher", "pixel",
     "product/priv-app/NexusLauncherRelease/NexusLauncherRelease.apk", "pixellauncher.apk", 30,
     {"requires": ["gmscore"]}),
]

EXTRA_FILES = [
    ("gmscore", "product/etc/permissions/privapp-permissions-google-p.xml",
     "gms-perms.xml", 4, "permission"),
    ("gmscore", "product/etc/sysconfig/google.xml", "gms-sysconfig.xml", 3, "sysconfig"),
]


def main(root: str) -> int:
    assets = os.path.join(root, "assets")
    os.makedirs(assets, exist_ok=True)
    rng = random.Random(42)

    def blob(name: str, kib: int) -> tuple[str, int]:
        data = bytes(rng.getrandbits(8) for _ in range(kib * 1024))
        with open(os.path.join(assets, name), "wb") as f:
            f.write(data)
        return hashlib.sha256(data).hexdigest(), len(data)

    def entry(path, asset, kib, kind="apk"):
        digest, size = blob(asset, kib)
        return {"path": path, "asset": asset, "sha256": digest, "size": size,
                "mode": "0644", "context": "u:object_r:system_file:s0", "kind": kind}

    packages = []
    for pid, name, category, path, asset, kib, extra in PACKAGES:
        pkg = {"id": pid, "name": name, "category": category,
               "files": [entry(path, asset, kib)]}
        pkg.update(extra)
        packages.append(pkg)

    by_id = {p["id"]: p for p in packages}
    for pid, path, asset, kib, kind in EXTRA_FILES:
        by_id[pid]["files"].append(entry(path, asset, kib, kind))

    release = {
        "id": "a16-bp41.250901.001",
        "android": {"api": 36, "version": "16", "codename": "Baklava"},
        "source": {
            "device": "comet", "build": "BP41.250901.001",
            "image": "https://example.invalid/comet-ota.zip", "sha256": "0" * 64,
        },
        "created": "2026-09-05T00:00:00Z",
        "asset_base": "assets",
    }

    with open(os.path.join(root, "manifest.json"), "w") as f:
        json.dump({"schema": 1, "release": release, "packages": packages}, f, indent=2)

    with open(os.path.join(root, "index.json"), "w") as f:
        json.dump({
            "schema": 1, "repo": "306gapps/306gapps-assets",
            "updated": release["created"],
            "releases": [{
                "id": release["id"], "android": release["android"],
                "device": "comet", "build": "BP41.250901.001", "branch": "a16",
                "created": release["created"],
                "manifest": "manifest.json", "asset_base": "assets",
            }],
        }, f, indent=2)

    print(f"{root}: {len(packages)} packages")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1] if len(sys.argv) > 1 else "fixture"))
