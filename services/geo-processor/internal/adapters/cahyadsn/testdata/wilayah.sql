-- Potongan buatan sendiri yang meniru format db/wilayah.sql github.com/cahyadsn/wilayah (MIT).
DROP TABLE IF EXISTS wilayah;
CREATE TABLE IF NOT EXISTS wilayah (
    kode varchar(13) NOT NULL,
    nama varchar(100) NOT NULL,
    PRIMARY KEY (kode)
) ENGINE=MyISAM;

-- Provinsi JAWA BARAT
INSERT INTO wilayah (kode, nama)
VALUES
('32','Jawa Barat'),
('32.73','Kota Bandung'),
('32.73.02','Coblong'),
('32.73.02.1001','Dago');
INSERT INTO wilayah (kode, nama)
VALUES
('53.07.01.2021','Regapu’u'),
('73.04.01.1001','Ma''rang');
