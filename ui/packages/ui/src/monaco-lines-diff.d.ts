// Monaco exposes this internal ESM subpath without a declaration.
declare module "monaco-editor/editor/common/diff/linesDiffComputers.js" {
  interface Range {
    startLineNumber: number;
    endLineNumberExclusive: number;
  }
  export const linesDiffComputers: {
    getDefault(): {
      computeDiff(original: string[], modified: string[], options: {
        maxComputationTimeMs: number;
        ignoreTrimWhitespace: boolean;
      }): {
        changes: Array<{ original: Range; modified: Range }>;
        hitTimeout: boolean;
      };
    };
  };
}
