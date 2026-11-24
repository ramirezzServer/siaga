/** Titik WGS84 seperti di kontrak OpenAPI. */
export interface Point {
  readonly latitude: number;
  readonly longitude: number;
}

/** [min_lon, min_lat, max_lon, max_lat] */
export type BBox = readonly [number, number, number, number];

/** GeoJSON MultiPolygon. */
export interface MultiPolygon {
  readonly type: "MultiPolygon";
  readonly coordinates: number[][][][];
}
