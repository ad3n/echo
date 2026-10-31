import argparse
import pathlib
import random
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument("before")
parser.add_argument("after")
parser.add_argument("output")
parser.add_argument("--benchmark", default="^BenchmarkProxyResponseBuffer$")
parser.add_argument("--cpu", default="1,4")
args = parser.parse_args()
output = pathlib.Path(args.output)
output.mkdir(parents=True, exist_ok=True)
rng = random.Random(20261031)
for name in ("before", "after"):
    (output / (name + ".txt")).write_text("")
for pair in range(12):
    order = ["before", "after"]
    rng.shuffle(order)
    for name in order:
        result = subprocess.run(
            [getattr(args, name), "-test.run=^$", "-test.bench=" + args.benchmark,
             "-test.benchmem", "-test.benchtime=250ms", "-test.cpu=" + args.cpu,
             "-test.count=1"],
            check=True, capture_output=True, text=True,
        )
        with (output / (name + ".txt")).open("a") as stream:
            stream.write(result.stdout)
    print(f"pair {pair + 1} complete", flush=True)
