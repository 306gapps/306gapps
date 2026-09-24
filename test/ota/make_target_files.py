#!/usr/bin/env python3
"""Write a small but structurally faithful target-files package for tests."""
import os, sys, zipfile

FILES = {
    "META/misc_info.txt": (
        "ab_update=true\nproduct_fs_type=erofs\n"
        "product_size=104857600\nsystem_size=209715200\n"
    ),
    "META/apkcerts.txt":
        'name="Dialer.apk" certificate="build/target/product/security/platform.x509.pem"'
        ' private_key="build/target/product/security/platform.pk8" partition="system_ext"\n',
    "META/product_filesystem_config.txt":
        "app 0 0 755 capabilities=0x0\n"
        "app/QuickSearchBox 0 0 755 capabilities=0x0\n"
        "app/QuickSearchBox/QuickSearchBox.apk 0 0 644 capabilities=0x0\n",
    "META/system_ext_filesystem_config.txt":
        "priv-app 0 0 755 capabilities=0x0\n"
        "priv-app/Dialer 0 0 755 capabilities=0x0\n"
        "priv-app/Dialer/Dialer.apk 0 0 644 capabilities=0x0\n",
    "META/ab_partitions.txt": "product\nsystem\nsystem_ext\n",
    "PRODUCT/app/QuickSearchBox/QuickSearchBox.apk": "stock-search-box",
    "PRODUCT/etc/build.prop": "ro.build.version.sdk=36\n",
    "SYSTEM_EXT/priv-app/Dialer/Dialer.apk": "stock-aosp-dialer",
    "SYSTEM/framework/framework.jar": "framework-bytes",
    "IMAGES/product.img": "stale-product-image",
    "META/care_map.pb": "stale-care-map",
}

def main(path):
    os.makedirs(os.path.dirname(path) or ".", exist_ok=True)
    with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as z:
        for name, content in FILES.items():
            z.writestr(name, content)
    print(f"{path}: {len(FILES)} entries")

if __name__ == "__main__":
    main(sys.argv[1] if len(sys.argv) > 1 else "base-target_files.zip")
