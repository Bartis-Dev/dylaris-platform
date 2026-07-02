import { API_URL, getAuthHeader } from "./core";
import type { Pack } from "./packs";

export async function setSolderConfig(
  packId: number,
  input: {
    solderSlug: string;
    solderDisplayName: string;
    recommendedBuild?: string;
    latestBuild?: string;
    private?: boolean;
  },
): Promise<{ success: boolean; pack?: Pack; message?: string }> {
  const res = await fetch(`${API_URL}/packs/${packId}/solder-config`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json", ...getAuthHeader() },
    body: JSON.stringify(input),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) return { success: false, message: data.error || "Failed to save Solder config" };
  return { success: true, pack: data.pack };
}

export async function publishSolder(
  packId: number,
  buildId: number,
): Promise<{ success: boolean; slug?: string; build?: string; message?: string }> {
  const res = await fetch(`${API_URL}/packs/${packId}/builds/${buildId}/publish-solder`, {
    method: "POST",
    headers: getAuthHeader(),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) return { success: false, message: data.error || "Failed to publish to Solder" };
  return { success: true, slug: data.slug, build: data.build };
}
