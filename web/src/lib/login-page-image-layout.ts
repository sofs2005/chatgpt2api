export const LOGIN_PAGE_IMAGE_MIN_ZOOM = 1;
export const LOGIN_PAGE_IMAGE_MAX_ZOOM = 3;
export const LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM = Object.freeze({
  zoom: 1,
  positionX: 50,
  positionY: 50,
});

export const LOGIN_PAGE_IMAGE_MODES = ["contain", "cover", "fill"] as const;
export type LoginPageImageMode = (typeof LOGIN_PAGE_IMAGE_MODES)[number];

type LoginPageImageTransform = {
  zoom?: number;
  positionX?: number;
  positionY?: number;
};

type LoginPageImageLayoutParams = {
  frameWidth: number;
  frameHeight: number;
  imageWidth: number;
  imageHeight: number;
  mode?: LoginPageImageMode | string;
  zoom?: number;
  positionX?: number;
  positionY?: number;
};

function clamp(value: number, min: number, max: number) {
  return Math.min(Math.max(value, min), max);
}

function roundTo(value: number, digits = 2) {
  const factor = 10 ** digits;
  return Math.round(value * factor) / factor;
}

export function isLoginPageImageMode(value: unknown): value is LoginPageImageMode {
  return typeof value === "string" && LOGIN_PAGE_IMAGE_MODES.includes(value as LoginPageImageMode);
}

export function normalizeLoginPageImageMode(value: unknown): LoginPageImageMode {
  return isLoginPageImageMode(value) ? value : "contain";
}

export function normalizeLoginPageImageTransform(value: LoginPageImageTransform = {}) {
  const zoom = Number(value.zoom);
  const positionX = Number(value.positionX);
  const positionY = Number(value.positionY);

  return {
    zoom: roundTo(
      clamp(
        Number.isFinite(zoom) ? zoom : LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.zoom,
        LOGIN_PAGE_IMAGE_MIN_ZOOM,
        LOGIN_PAGE_IMAGE_MAX_ZOOM,
      ),
    ),
    positionX: roundTo(
      clamp(Number.isFinite(positionX) ? positionX : LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.positionX, 0, 100),
    ),
    positionY: roundTo(
      clamp(Number.isFinite(positionY) ? positionY : LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.positionY, 0, 100),
    ),
  };
}

export function getLoginPageImageLayout({
  frameWidth,
  frameHeight,
  imageWidth,
  imageHeight,
  mode = "contain",
  zoom = LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.zoom,
  positionX = LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.positionX,
  positionY = LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.positionY,
}: LoginPageImageLayoutParams) {
  if (!frameWidth || !frameHeight || !imageWidth || !imageHeight) {
    return null;
  }

  const normalized = normalizeLoginPageImageTransform({ zoom, positionX, positionY });
  let baseWidth = frameWidth;
  let baseHeight = frameHeight;

  if (mode === "cover" || mode === "contain") {
    const scale =
      mode === "cover"
        ? Math.max(frameWidth / imageWidth, frameHeight / imageHeight)
        : Math.min(frameWidth / imageWidth, frameHeight / imageHeight);

    baseWidth = imageWidth * scale;
    baseHeight = imageHeight * scale;
  }

  const width = baseWidth * normalized.zoom;
  const height = baseHeight * normalized.zoom;
  const availableX = frameWidth - width;
  const availableY = frameHeight - height;
  const x = availableX * (normalized.positionX / 100);
  const y = availableY * (normalized.positionY / 100);

  return {
    width,
    height,
    x,
    y,
    availableX,
    availableY,
  };
}

export function getLoginPageImagePositionPercentFromOffset(offset: number, availableSpace: number, fallback = 50) {
  if (!Number.isFinite(availableSpace) || Math.abs(availableSpace) < 0.0001) {
    return fallback;
  }

  return roundTo(clamp((offset / availableSpace) * 100, 0, 100));
}
