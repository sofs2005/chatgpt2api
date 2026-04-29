"use client";

import { useEffect, useRef, useState } from "react";
import { ImageIcon, LoaderCircle, RotateCcw, Save, Upload } from "lucide-react";
import { toast } from "sonner";

import { LoginPageImageEditor } from "@/components/login-page-image-editor";
import { Button } from "@/components/ui/button";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { DEFAULT_LOGIN_PAGE_IMAGE } from "@/lib/app-meta";
import {
  LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM,
  LOGIN_PAGE_IMAGE_MODES,
  normalizeLoginPageImageMode,
  type LoginPageImageMode,
} from "@/lib/login-page-image-layout";

import { useSettingsStore } from "../store";
import { SettingsCard, settingsInputClassName } from "./settings-ui";

const maxLoginPageImageSize = 10 * 1024 * 1024;
const modeLabels: Record<LoginPageImageMode, string> = {
  contain: "适应",
  cover: "铺满",
  fill: "拉伸",
};

function numberOrDefault(value: unknown, fallback: number) {
  const normalized = Number(value);
  return Number.isFinite(normalized) ? normalized : fallback;
}

export function LoginPageImageCard() {
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const [pendingFile, setPendingFile] = useState<File | null>(null);
  const [pendingPreviewUrl, setPendingPreviewUrl] = useState("");
  const config = useSettingsStore((state) => state.config);
  const isLoadingConfig = useSettingsStore((state) => state.isLoadingConfig);
  const isSavingConfig = useSettingsStore((state) => state.isSavingConfig);
  const setLoginPageImageUrl = useSettingsStore((state) => state.setLoginPageImageUrl);
  const setLoginPageImageMode = useSettingsStore((state) => state.setLoginPageImageMode);
  const setLoginPageImageTransform = useSettingsStore((state) => state.setLoginPageImageTransform);
  const restoreDefaultLoginPageImage = useSettingsStore((state) => state.restoreDefaultLoginPageImage);
  const saveLoginPageImage = useSettingsStore((state) => state.saveLoginPageImage);

  useEffect(() => {
    return () => {
      if (pendingPreviewUrl.startsWith("blob:")) {
        URL.revokeObjectURL(pendingPreviewUrl);
      }
    };
  }, [pendingPreviewUrl]);

  if (isLoadingConfig || !config) {
    return (
      <SettingsCard
        icon={ImageIcon}
        title="登录页图片"
        description="配置登录页右侧展示图。"
        tone="violet"
      >
        <div className="flex items-center justify-center py-10">
          <LoaderCircle className="size-5 animate-spin text-muted-foreground" />
        </div>
      </SettingsCard>
    );
  }

  const imageUrl = String(config.login_page_image_url || "");
  const mode = normalizeLoginPageImageMode(config.login_page_image_mode);
  const zoom = numberOrDefault(config.login_page_image_zoom, LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.zoom);
  const positionX = numberOrDefault(config.login_page_image_position_x, LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.positionX);
  const positionY = numberOrDefault(config.login_page_image_position_y, LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.positionY);
  const previewUrl = pendingPreviewUrl || imageUrl || DEFAULT_LOGIN_PAGE_IMAGE;

  const clearPendingFile = () => {
    setPendingFile(null);
    setPendingPreviewUrl((current) => {
      if (current.startsWith("blob:")) {
        URL.revokeObjectURL(current);
      }
      return "";
    });
  };

  return (
    <SettingsCard
      icon={ImageIcon}
      title="登录页图片"
      description="配置登录页右侧展示图。"
      tone="violet"
      action={
        <Button
          size="lg"
          onClick={() => {
            void saveLoginPageImage({
              file: pendingFile,
              action: pendingFile ? "replace" : imageUrl.trim() ? "keep" : "remove",
            }).then((saved) => {
              if (saved) {
                clearPendingFile();
              }
            });
          }}
          disabled={isSavingConfig}
        >
          {isSavingConfig ? (
            <LoaderCircle data-icon="inline-start" className="animate-spin" />
          ) : (
            <Save data-icon="inline-start" />
          )}
          保存
        </Button>
      }
    >
      <div className="flex flex-col gap-5">
        <section className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_10rem]">
          <Field className="gap-1.5">
            <FieldLabel htmlFor="settings-login-image-url">图片地址</FieldLabel>
            <Input
              id="settings-login-image-url"
              value={imageUrl}
              onChange={(event) => {
                clearPendingFile();
                setLoginPageImageUrl(event.target.value);
              }}
              placeholder="/login-page-images/example.png 或 https://example.com/image.png"
              className={settingsInputClassName}
            />
          </Field>
          <Field className="gap-1.5">
            <FieldLabel htmlFor="settings-login-image-mode">展示模式</FieldLabel>
            <Select value={mode} onValueChange={(value) => setLoginPageImageMode(normalizeLoginPageImageMode(value))}>
              <SelectTrigger id="settings-login-image-mode">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {LOGIN_PAGE_IMAGE_MODES.map((item) => (
                  <SelectItem key={item} value={item}>
                    {modeLabels[item]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        </section>

        <div className="flex flex-wrap gap-2">
          <input
            ref={fileInputRef}
            type="file"
            accept="image/*"
            className="hidden"
            onChange={(event) => {
              const file = event.target.files?.[0];
              event.target.value = "";
              if (!file) {
                return;
              }
              if (!file.type.startsWith("image/")) {
                toast.error("请选择图片文件");
                return;
              }
              if (file.size > maxLoginPageImageSize) {
                toast.error("登录页图片不能超过 10MB");
                return;
              }
              setPendingFile(file);
              setPendingPreviewUrl((current) => {
                if (current.startsWith("blob:")) {
                  URL.revokeObjectURL(current);
                }
                return URL.createObjectURL(file);
              });
              setLoginPageImageTransform(LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM);
              toast.success("图片已选择，保存后生效");
            }}
          />
          <Button type="button" variant="outline" onClick={() => fileInputRef.current?.click()} disabled={isSavingConfig}>
            <Upload data-icon="inline-start" />
            选择图片
          </Button>
          <Button
            type="button"
            variant="outline"
            onClick={() => {
              clearPendingFile();
              restoreDefaultLoginPageImage();
            }}
            disabled={isSavingConfig || (!imageUrl && !pendingFile)}
          >
            <RotateCcw data-icon="inline-start" />
            恢复默认
          </Button>
          {pendingFile ? (
            <span className="inline-flex min-h-9 items-center rounded-full bg-muted px-3 text-xs font-medium text-muted-foreground">
              待保存：{pendingFile.name}
            </span>
          ) : null}
        </div>

        <LoginPageImageEditor
          src={previewUrl}
          mode={mode}
          zoom={zoom}
          positionX={positionX}
          positionY={positionY}
          onChange={(transform) => setLoginPageImageTransform(transform)}
        />
      </div>
    </SettingsCard>
  );
}
