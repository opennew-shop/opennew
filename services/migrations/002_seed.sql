-- ============================================================================
-- ANCF Commerce - Seed Data for Phase 1
-- Version: 002
-- Description: Inserts GPU compute SKU catalog data for Phase 1 demo.
--              Includes H100, A100, and L40S GPU compute rental products.
-- ============================================================================

BEGIN;

-- Ensure idempotent: clear existing seed data by sku_id prefix
DELETE FROM catalog_skus WHERE sku_id LIKE 'sku_gpu_%';

-- ----------------------------------------------------------------------------
-- GPU Compute SKU Seed Data
-- ----------------------------------------------------------------------------

-- SKU 1: NVIDIA H100 80GB SXM5 - Premium hourly compute
INSERT INTO catalog_skus (
    sku_id, title, description, currency,
    price_amount_minor, price_scale,
    stock, stock_hint,
    specs, media, status
) VALUES (
    'sku_gpu_h100_v1',
    'NVIDIA H100 80GB SXM5 Compute - Hourly Rental',
    'High-performance GPU compute instance with NVIDIA H100 80GB SXM5. Ideal for LLM training, large-scale inference, and scientific computing. Includes 1x H100 GPU, 12 vCPU, 128GB RAM, 1TB NVMe SSD.',
    'vUSDC',
    2450000, 6,
    42, 42,
    '{
        "GPU": "NVIDIA H100 80GB SXM5",
        "GPU_Memory": "80GB HBM3",
        "vCPU": 12,
        "RAM": "128GB",
        "Storage": "1TB NVMe SSD",
        "Network": "25Gbps",
        "CUDA": "12.4",
        "TFLOPS_FP16": 989,
        "Interconnect": "NVLink 900GB/s"
    }'::jsonb,
    '{
        "thumbnail": "https://opennew.shop/assets/products/h100.svg",
        "banner": "https://opennew.shop/assets/products/h100.svg",
        "datasheet": "https://cdn.yourshop.com/docs/h100-datasheet.pdf"
    }'::jsonb,
    'active'
);

-- SKU 2: NVIDIA A100 80GB - Cost-effective hourly compute
INSERT INTO catalog_skus (
    sku_id, title, description, currency,
    price_amount_minor, price_scale,
    stock, stock_hint,
    specs, media, status
) VALUES (
    'sku_gpu_a100_v1',
    'NVIDIA A100 80GB Compute - Hourly Rental',
    'Reliable GPU compute with NVIDIA A100 80GB. Suitable for deep learning training, inference serving, and HPC workloads. Includes 1x A100 GPU, 8 vCPU, 64GB RAM, 500GB NVMe SSD.',
    'vUSDC',
    1500000, 6,
    28, 28,
    '{
        "GPU": "NVIDIA A100 80GB",
        "GPU_Memory": "80GB HBM2e",
        "vCPU": 8,
        "RAM": "64GB",
        "Storage": "500GB NVMe SSD",
        "Network": "10Gbps",
        "CUDA": "12.4",
        "TFLOPS_FP16": 312,
        "Interconnect": "NVLink 600GB/s"
    }'::jsonb,
    '{
        "thumbnail": "https://opennew.shop/assets/products/a100.svg",
        "banner": "https://opennew.shop/assets/products/a100.svg",
        "datasheet": "https://cdn.yourshop.com/docs/a100-datasheet.pdf"
    }'::jsonb,
    'active'
);

-- SKU 3: NVIDIA L40S - Value-oriented inference compute
INSERT INTO catalog_skus (
    sku_id, title, description, currency,
    price_amount_minor, price_scale,
    stock, stock_hint,
    specs, media, status
) VALUES (
    'sku_gpu_l40s_v1',
    'NVIDIA L40S Compute - Hourly Rental',
    'Cost-efficient GPU compute with NVIDIA L40S 48GB. Optimized for inference serving, fine-tuning, and medium-scale workloads. Includes 1x L40S GPU, 8 vCPU, 48GB RAM, 250GB NVMe SSD.',
    'vUSDC',
    800000, 6,
    56, 56,
    '{
        "GPU": "NVIDIA L40S 48GB",
        "GPU_Memory": "48GB GDDR6",
        "vCPU": 8,
        "RAM": "48GB",
        "Storage": "250GB NVMe SSD",
        "Network": "10Gbps",
        "CUDA": "12.4",
        "TFLOPS_FP16": 362,
        "Tensor_Cores": "4th gen"
    }'::jsonb,
    '{
        "thumbnail": "https://opennew.shop/assets/products/l40s.svg",
        "banner": "https://opennew.shop/assets/products/l40s.svg",
        "datasheet": "https://cdn.yourshop.com/docs/l40s-datasheet.pdf"
    }'::jsonb,
    'active'
);

COMMIT;
