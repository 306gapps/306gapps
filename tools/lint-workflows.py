#!/usr/bin/env python3
"""Reject a workflow with a duplicate key.

PyYAML and actionlint both accept duplicate mapping keys and quietly keep the
last one. GitHub does not: it refuses to parse the file, and the only symptom
is the workflow losing its name and becoming undispatchable. That is a bad way
to find out, so check for it here.
"""
import sys
import yaml


class Strict(yaml.SafeLoader):
    pass


def no_duplicates(loader, node, deep=False):
    mapping = {}
    for key_node, value_node in node.value:
        key = loader.construct_object(key_node, deep=deep)
        if key in mapping:
            raise yaml.constructor.ConstructorError(
                None, None, f"duplicate key {key!r}", key_node.start_mark)
        mapping[key] = loader.construct_object(value_node, deep=deep)
    return mapping


Strict.add_constructor(
    yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, no_duplicates)


def main(paths: list[str]) -> int:
    bad = 0
    for p in paths:
        try:
            yaml.load(open(p), Loader=Strict)
        except yaml.YAMLError as e:
            print(f"{p}: {e}", file=sys.stderr)
            bad = 1
        else:
            print(f"ok {p}")
    return bad


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
