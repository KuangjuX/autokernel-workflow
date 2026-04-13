#!/usr/bin/env python3
"""Tests for init_kernel.py"""

import os
import sys
import tempfile
import textwrap
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from init_kernel import (
    load_spec,
    generate_reference,
    generate_triton_kernel,
    generate_cuda_kernel,
    resolve_dtype,
    verify_syntax,
)


def _write_yaml(d: Path, content: str) -> Path:
    p = d / "kernel.yaml"
    p.write_text(textwrap.dedent(content))
    return p


def test_resolve_dtype():
    assert resolve_dtype("bf16") == "torch.bfloat16"
    assert resolve_dtype("fp32") == "torch.float32"
    assert resolve_dtype("int64") == "torch.int64"
    assert resolve_dtype("fp8") == "torch.float8_e4m3fn"
    print("  PASS test_resolve_dtype")


def test_load_spec_valid():
    with tempfile.TemporaryDirectory() as d:
        _write_yaml(Path(d), """\
            name: test_kernel
            backend: triton
            dims: {M: 1024, N: 512}
            inputs:
              - {name: x, dtype: bf16, shape: [M, N]}
        """)
        spec = load_spec(Path(d) / "kernel.yaml")
        assert spec["name"] == "test_kernel"
        assert spec["backend"] == "triton"
        assert spec["dims"]["M"] == 1024
        assert len(spec["inputs"]) == 1
    print("  PASS test_load_spec_valid")


def test_generate_reference():
    spec = {
        "name": "test_norm",
        "backend": "triton",
        "dims": {"M": 8192, "N": 2048},
        "inputs": [
            {"name": "x", "dtype": "bf16", "shape": ["M", "N"]},
            {"name": "gamma", "dtype": "bf16", "shape": ["N"]},
        ],
    }
    content = generate_reference(spec)
    assert "class Model(nn.Module):" in content
    assert "def forward(self, x: torch.Tensor, gamma: torch.Tensor)" in content
    assert "def get_inputs(shape_idx=None):" in content
    assert "def get_init_inputs():" in content
    assert "M = 8192" in content
    assert "N = 2048" in content
    assert "SHAPE_CONFIGS" in content
    assert "NotImplementedError" in content

    compile(content, "reference.py", "exec")
    print("  PASS test_generate_reference")


def test_generate_triton_kernel_no_source():
    spec = {
        "name": "my_norm",
        "backend": "triton",
        "dims": {"M": 4096, "N": 1024},
        "inputs": [
            {"name": "x", "dtype": "bf16", "shape": ["M", "N"]},
        ],
    }
    content = generate_triton_kernel(spec, None)
    assert "class Model(nn.Module):" in content
    assert "@triton.jit" in content
    assert "def get_inputs():" in content
    assert "M = 4096" in content

    compile(content, "kernel.py", "exec")
    print("  PASS test_generate_triton_kernel_no_source")


def test_generate_triton_kernel_with_source():
    source = textwrap.dedent("""\
        import triton
        import triton.language as tl

        @triton.jit
        def _my_kernel(x_ptr, out_ptr, n: int):
            pid = tl.program_id(0)
            x = tl.load(x_ptr + pid)
            tl.store(out_ptr + pid, x * 2)
    """)
    spec = {
        "name": "double",
        "backend": "triton",
        "dims": {"N": 1024},
        "inputs": [{"name": "x", "dtype": "fp32", "shape": ["N"]}],
    }
    content = generate_triton_kernel(spec, source)
    assert "class Model(nn.Module):" in content
    assert "@triton.jit" in content
    assert "_my_kernel" in content
    assert "def get_inputs():" in content

    compile(content, "kernel.py", "exec")
    print("  PASS test_generate_triton_kernel_with_source")


def test_generate_triton_kernel_complete_source():
    """Source already has Model class -- should be used as-is."""
    source = textwrap.dedent("""\
        import torch
        import torch.nn as nn
        import triton
        import triton.language as tl

        @triton.jit
        def _k(x_ptr, n: int):
            pass

        class Model(nn.Module):
            def __init__(self):
                super().__init__()
            def forward(self, x):
                return x
    """)
    spec = {
        "name": "passthrough",
        "backend": "triton",
        "dims": {"N": 100},
        "inputs": [{"name": "x", "dtype": "fp32", "shape": ["N"]}],
    }
    content = generate_triton_kernel(spec, source)
    assert "class Model" in content
    assert content.count("class Model") == 1
    print("  PASS test_generate_triton_kernel_complete_source")


def test_generate_cuda_kernel_no_source():
    spec = {
        "name": "my_cuda_op",
        "backend": "cuda",
        "dims": {"M": 1024},
        "inputs": [{"name": "x", "dtype": "fp32", "shape": ["M"]}],
    }
    content = generate_cuda_kernel(spec, None)
    assert "load_inline" in content
    assert "class Model(nn.Module):" in content
    assert "self.custom_op" in content
    assert "cuda_source" in content
    assert "def get_inputs():" in content

    compile(content, "kernel.py", "exec")
    print("  PASS test_generate_cuda_kernel_no_source")


def test_generate_cuda_kernel_with_source():
    cuda_src = textwrap.dedent("""\
        #include <torch/extension.h>
        __global__ void my_kernel(float* x, int n) {
            int i = threadIdx.x + blockIdx.x * blockDim.x;
            if (i < n) x[i] *= 2.0f;
        }
        torch::Tensor my_cuda_op_cuda(torch::Tensor x) {
            my_kernel<<<1, 256>>>(x.data_ptr<float>(), x.numel());
            return x;
        }
    """)
    spec = {
        "name": "my_cuda_op",
        "backend": "cuda",
        "dims": {"N": 1024},
        "inputs": [{"name": "x", "dtype": "fp32", "shape": ["N"]}],
    }
    content = generate_cuda_kernel(spec, cuda_src)
    assert "my_kernel" in content
    assert "load_inline" in content
    assert "my_cuda_op_cuda" in content

    compile(content, "kernel.py", "exec")
    print("  PASS test_generate_cuda_kernel_with_source")


def test_generate_cuda_kernel_with_includes():
    spec = {
        "name": "my_kittens_op",
        "backend": "cuda",
        "dims": {"M": 1024},
        "inputs": [{"name": "x", "dtype": "fp32", "shape": ["M"]}],
        "cuda": {
            "extra_include_paths": ["/opt/kittens/include"],
            "extra_cuda_cflags": ["-O3", "-std=c++20", "-DKITTENS_HOPPER"],
        },
    }
    content = generate_cuda_kernel(spec, None)
    assert '"/opt/kittens/include"' in content
    assert "-std=c++20" in content
    assert "-DKITTENS_HOPPER" in content
    print("  PASS test_generate_cuda_kernel_with_includes")


def test_int_input():
    spec = {
        "name": "scatter",
        "backend": "triton",
        "dims": {"M": 100, "K": 8},
        "inputs": [
            {"name": "indices", "dtype": "int64", "shape": ["M", "K"]},
            {"name": "values", "dtype": "fp32", "shape": ["M", "K"]},
        ],
    }
    content = generate_reference(spec)
    assert "torch.randint" in content
    assert "torch.randn" in content
    print("  PASS test_int_input")


def test_end_to_end_triton():
    """Full workflow: write yaml + source, run generation, check output."""
    with tempfile.TemporaryDirectory() as d:
        pkg = Path(d) / "test_pkg"
        pkg.mkdir()

        (pkg / "kernel.yaml").write_text(textwrap.dedent("""\
            name: test_add
            backend: triton
            dims: {N: 1024}
            inputs:
              - {name: a, dtype: fp32, shape: [N]}
              - {name: b, dtype: fp32, shape: [N]}
        """))

        # Run the main flow via subprocess
        import subprocess
        result = subprocess.run(
            [sys.executable, str(Path(__file__).parent / "init_kernel.py"), str(pkg)],
            capture_output=True, text=True,
        )
        print(f"    stdout: {result.stdout.strip()}")
        if result.returncode != 0:
            print(f"    stderr: {result.stderr.strip()}")
        assert result.returncode == 0, f"init_kernel.py failed: {result.stderr}"

        assert (pkg / "kernel.py").exists()
        assert (pkg / "reference.py").exists()

        kernel_content = (pkg / "kernel.py").read_text()
        ref_content = (pkg / "reference.py").read_text()

        assert "class Model" in kernel_content
        assert "class Model" in ref_content
        assert "N = 1024" in kernel_content
        assert "N = 1024" in ref_content
    print("  PASS test_end_to_end_triton")


def test_end_to_end_cuda():
    with tempfile.TemporaryDirectory() as d:
        pkg = Path(d) / "cuda_pkg"
        pkg.mkdir()

        (pkg / "my_op.cuh").write_text(textwrap.dedent("""\
            #include <torch/extension.h>
            torch::Tensor my_op_cuda(torch::Tensor x) { return x; }
        """))

        (pkg / "kernel.yaml").write_text(textwrap.dedent("""\
            name: my_op
            backend: cuda
            dims: {N: 512}
            inputs:
              - {name: x, dtype: bf16, shape: [N]}
            kernel_source: my_op.cuh
        """))

        import subprocess
        result = subprocess.run(
            [sys.executable, str(Path(__file__).parent / "init_kernel.py"), str(pkg)],
            capture_output=True, text=True,
        )
        print(f"    stdout: {result.stdout.strip()}")
        assert result.returncode == 0, f"failed: {result.stderr}"

        kernel_content = (pkg / "kernel.py").read_text()
        assert "load_inline" in kernel_content
        assert "my_op_cuda" in kernel_content
        assert "self.custom_op" in kernel_content
    print("  PASS test_end_to_end_cuda")


if __name__ == "__main__":
    tests = [
        test_resolve_dtype,
        test_load_spec_valid,
        test_generate_reference,
        test_generate_triton_kernel_no_source,
        test_generate_triton_kernel_with_source,
        test_generate_triton_kernel_complete_source,
        test_generate_cuda_kernel_no_source,
        test_generate_cuda_kernel_with_source,
        test_generate_cuda_kernel_with_includes,
        test_int_input,
        test_end_to_end_triton,
        test_end_to_end_cuda,
    ]
    passed = 0
    failed = 0
    for t in tests:
        try:
            t()
            passed += 1
        except Exception as e:
            print(f"  FAIL {t.__name__}: {e}")
            failed += 1

    print(f"\n{'='*40}")
    print(f"  {passed} passed, {failed} failed")
    if failed:
        sys.exit(1)
