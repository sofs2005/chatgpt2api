import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  fetchAwesomeGptImage2ChGalleryPrompts,
  fetchAwesomeGptImage2GalleryPrompts,
  fetchAwesomeGptImage2Prompts,
} from "@/app/image/banana-prompts";

const apiAndPromptsMarkdown = `## E-commerce Cases

### Case 151: [Miniature Diorama Skincare Advertisement](https://example.com/case-151)

| Output |
| :----: |
| <a href="https://example.com/output"><img src="https://raw.githubusercontent.com/EvoLinkAI/awesome-gpt-image-2-API-and-Prompts/main/images/ecommerce_case151/output.jpg" width="300" alt="output"></a> |

**Prompt:**

\`\`\`
Create a miniature skincare ad.
\`\`\`
`;

const galleryMarkdown = `## 信息图与可视化

### 例 1：信息图可视化设计

![城市代谢图](../data/images/case1.jpg)

**提示词：**

\`\`\`text
生成一张信息图海报
\`\`\`
`;

describe("prompt market parsers", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("parses API-and-Prompts cases even when the heading has no author suffix", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(apiAndPromptsMarkdown, { status: 200 }))
      .mockResolvedValueOnce(new Response(apiAndPromptsMarkdown, { status: 200 }));

    vi.stubGlobal("fetch", fetchMock);

    const prompts = await fetchAwesomeGptImage2Prompts();

    expect(prompts).toHaveLength(1);
    expect(prompts[0]).toMatchObject({
      source: "awesome-gpt-image-2-prompts",
      title: "Miniature Diorama Skincare Advertisement",
      author: "Community",
      prompt: "Create a miniature skincare ad.",
      preview:
        "https://raw.githubusercontent.com/sofs2005/awesome-gpt-image-2-API-and-Prompts/main/images/ecommerce_case151/output.jpg",
    });
  });

  it("parses gallery cases with Chinese headings and markdown image syntax", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(galleryMarkdown, { status: 200 }))
      .mockResolvedValueOnce(new Response(galleryMarkdown, { status: 200 }));

    vi.stubGlobal("fetch", fetchMock);

    const prompts = await fetchAwesomeGptImage2GalleryPrompts();

    expect(prompts).toHaveLength(2);
    expect(prompts[0]).toMatchObject({
      source: "awesome-gpt-image-2",
      title: "信息图可视化设计",
      author: "Community",
      prompt: "生成一张信息图海报",
      preview: "https://raw.githubusercontent.com/freestylefly/awesome-gpt-image-2/main/data/images/case1.jpg",
    });
  });

  it("parses Chinese gallery cases into the zh-CN source", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(galleryMarkdown, { status: 200 }))
      .mockResolvedValueOnce(new Response(galleryMarkdown, { status: 200 }));

    vi.stubGlobal("fetch", fetchMock);

    const prompts = await fetchAwesomeGptImage2ChGalleryPrompts();

    expect(prompts).toHaveLength(2);
    expect(prompts[0]).toMatchObject({
      source: "awesome-gpt-image-2-ch",
      title: "信息图可视化设计",
      prompt: "生成一张信息图海报",
      preview: "https://raw.githubusercontent.com/freestylefly/awesome-gpt-image-2/main/data/images/case1.jpg",
      localizations: {
        "zh-CN": {
          title: "信息图可视化设计",
          prompt: "生成一张信息图海报",
          category: "信息图与可视化",
          subCategory: "Case 1",
        },
      },
    });
  });
});
