/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly DEV: boolean;
  readonly VITE_REACT_GRAB?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

interface StaticImageAsset {
  src: string;
  height?: number;
  width?: number;
  blurDataURL?: string;
}

declare module "*.png" {
  const src: string | StaticImageAsset;
  export default src;
}
