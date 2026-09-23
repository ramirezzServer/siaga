/*
copyright (c) 2025 by cahya dsn; nilai (c) di komentar tidak boleh dianggap tuple
*/
-- MySQL
DROP TABLE IF EXISTS `wilayah_boundaries`;
CREATE TABLE `wilayah_boundaries` (
  `kode` varchar(13) NOT NULL,
  `path` longtext DEFAULT NULL
);
INSERT INTO wilayah_boundaries(kode,nama,lat,lng,path) VALUES
	('31','DKI Jakarta',-6.2,106.8,'[[[-6.3,106.7],[-6.3,106.9],[-6.1,106.9],[-6.3,106.7]]]'),
	('32','Jawa Barat',-6.9,107.6,'[[[-7.9,106.0],[-7.9,108.9],[-5.9,108.9],[-5.9,106.0],[-7.9,106.0]]]');
